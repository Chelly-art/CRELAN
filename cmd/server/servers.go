package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type serverService struct {
	auth        *authService
	deployments *deploymentService
}

type serverCreateRequest struct {
	Name          string `json:"name"`
	Mode          string `json:"mode"`
	Region        string `json:"region"`
	InstanceType  string `json:"instance_type"`
	RoleARN       string `json:"role_arn"`
	ExternalID    string `json:"external_id"`
	SSHSourceCIDR string `json:"ssh_source_cidr"`
}

type customerServer struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Mode          string `json:"mode"`
	Region        string `json:"region"`
	InstanceType  string `json:"instance_type"`
	InstanceID    string `json:"instance_id,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	PublicIP      string `json:"public_ip,omitempty"`
	PublicDNS     string `json:"public_dns,omitempty"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message,omitempty"`
	CreatedAt     string `json:"created_at"`
}

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-[0-9]+$`)
var serverInstanceTypePattern = regexp.MustCompile(`^t3\.(micro|small|medium|large)$`)

func (s *serverService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/aws/connection", s.awsConnection)
	mux.HandleFunc("GET /api/v1/servers", s.list)
	mux.HandleFunc("POST /api/v1/servers", s.create)
	mux.HandleFunc("DELETE /api/v1/servers/{id}", s.delete)
	mux.HandleFunc("POST /api/v1/servers/{id}/stop", s.stop)
	mux.HandleFunc("POST /api/v1/servers/{id}/start", s.start)
	mux.HandleFunc("POST /api/v1/servers/{id}/deployments", s.deployImage)
	mux.HandleFunc("GET /api/v1/servers/{id}/deployments", s.listDeployments)
	mux.HandleFunc("GET /api/v1/agent/commands", s.agentNextCommand)
	mux.HandleFunc("POST /api/v1/agent/commands/{command_id}/result", s.agentCommandResult)
	mux.HandleFunc("POST /api/v1/agent/heartbeat", s.agentHeartbeat)
	mux.HandleFunc("GET /downloads/crelan-agent", s.downloadAgent)
}

func (s *serverService) awsClient(ctx context.Context, mode, roleARN, externalID, region string) (*ec2.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	if mode == "customer" {
		if strings.TrimSpace(roleARN) == "" || strings.TrimSpace(externalID) == "" {
			return nil, errors.New("customer AWS role and external ID are required")
		}
		stsClient := sts.NewFromConfig(cfg)
		cfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
			o.ExternalID = aws.String(externalID)
			o.RoleSessionName = "crelan-server-provision"
		}))
	}
	return ec2.NewFromConfig(cfg), nil
}

func (s *serverService) create(w http.ResponseWriter, r *http.Request) {
	principal, err := s.deployments.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in or provide a workspace token")
		return
	}
	if !principal.hasScope("deployments:write") {
		writeError(w, http.StatusForbidden, "Workspace role cannot create servers")
		return
	}
	var input serverCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid server request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Mode = strings.TrimSpace(input.Mode)
	input.Region = strings.TrimSpace(input.Region)
	input.InstanceType = strings.TrimSpace(input.InstanceType)
	input.RoleARN = strings.TrimSpace(input.RoleARN)
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.SSHSourceCIDR = strings.TrimSpace(input.SSHSourceCIDR)
	if !deploymentNamePattern.MatchString(input.Name) {
		writeError(w, http.StatusBadRequest, "Server name must use lowercase letters, numbers, and hyphens")
		return
	}
	if input.Mode != "customer" && input.Mode != "managed" {
		writeError(w, http.StatusBadRequest, "Choose customer or Crelan managed AWS")
		return
	}
	if !awsRegionPattern.MatchString(input.Region) {
		writeError(w, http.StatusBadRequest, "Enter a valid AWS region")
		return
	}
	if input.InstanceType == "" {
		input.InstanceType = "t3.small"
	}
	if !serverInstanceTypePattern.MatchString(input.InstanceType) {
		writeError(w, http.StatusBadRequest, "Choose a supported t3 instance size")
		return
	}
	if input.SSHSourceCIDR == "" {
		writeError(w, http.StatusBadRequest, "Enter an allowed SSH source CIDR (for example, your-ip/32)")
		return
	}
	if _, _, err := netParseCIDR(input.SSHSourceCIDR); err != nil {
		writeError(w, http.StatusBadRequest, "SSH source must be a valid IPv4 or IPv6 CIDR")
		return
	}
	if input.Mode == "customer" && (input.RoleARN == "" || input.ExternalID == "") {
		_ = s.auth.db.QueryRowContext(r.Context(), `SELECT role_arn,external_id FROM workspace_aws_connections WHERE workspace_id=?`, principal.workspaceID).Scan(&input.RoleARN, &input.ExternalID)
		input.RoleARN = strings.TrimSpace(input.RoleARN)
		input.ExternalID = strings.TrimSpace(input.ExternalID)
		if input.RoleARN == "" || input.ExternalID == "" {
			writeError(w, http.StatusBadRequest, "Connect your AWS account with a role ARN and external ID")
			return
		}
	}
	if input.Mode == "managed" && !strings.EqualFold(getenv("CRELAN_MANAGED_AWS_ENABLED"), "true") {
		writeError(w, http.StatusServiceUnavailable, "Crelan managed servers are not configured yet")
		return
	}
	publicURL, urlErr := url.Parse(strings.TrimSpace(getenv("CRELAN_PUBLIC_URL")))
	if urlErr != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		writeError(w, http.StatusServiceUnavailable, "Server provisioning needs an HTTPS CRELAN_PUBLIC_URL so the VM agent can connect securely")
		return
	}
	if strings.TrimSpace(getenv("CRELAN_AGENT_BINARY_PATH")) == "" {
		writeError(w, http.StatusServiceUnavailable, "Server provisioning needs CRELAN_AGENT_BINARY_PATH for the VM agent download")
		return
	}
	var existingID int64
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT id FROM servers WHERE workspace_id = ? AND name = ?`, principal.workspaceID, input.Name).Scan(&existingID); err == nil {
		writeError(w, http.StatusConflict, "A server with this name already exists")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Could not check server name")
		return
	}
	client, err := s.awsClient(r.Context(), input.Mode, input.RoleARN, input.ExternalID, input.Region)
	if err != nil {
		log.Printf("configure server AWS client: %v", err)
		writeError(w, http.StatusBadRequest, "Could not connect to AWS with these settings")
		return
	}
	server, err := s.provision(r.Context(), client, principal.workspaceID, input)
	if err != nil {
		log.Printf("provision server for workspace %d: %v", principal.workspaceID, err)
		writeError(w, http.StatusBadGateway, "AWS could not provision this server. Check the account role, default VPC, quota, and region.")
		return
	}
	if input.Mode == "customer" {
		_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO workspace_aws_connections(workspace_id,role_arn,external_id,updated_at) VALUES(?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET role_arn=excluded.role_arn,external_id=excluded.external_id,updated_at=excluded.updated_at`, principal.workspaceID, input.RoleARN, input.ExternalID, time.Now().UTC().Format(time.RFC3339Nano))
	}
	writeJSON(w, http.StatusCreated, server)
}

func (s *serverService) awsConnection(w http.ResponseWriter, r *http.Request) {
	p, err := s.deployments.authenticate(r)
	if err != nil {
		writeError(w, 401, "Sign in required")
		return
	}
	var roleARN, externalID string
	err = s.auth.db.QueryRowContext(r.Context(), `SELECT role_arn,external_id FROM workspace_aws_connections WHERE workspace_id=?`, p.workspaceID).Scan(&roleARN, &externalID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"connected": false})
		return
	}
	if err != nil {
		writeError(w, 500, "Could not load AWS connection")
		return
	}
	writeJSON(w, 200, map[string]any{"connected": true, "role_arn": roleARN, "external_id": externalID})
}

func (s *serverService) provision(ctx context.Context, client *ec2.Client, workspaceID int64, input serverCreateRequest) (customerServer, error) {
	// Use an AWS-published Amazon Linux 2023 x86_64 AMI, discovered per region.
	images, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{Owners: []string{"amazon"}, Filters: []types.Filter{{Name: aws.String("name"), Values: []string{"al2023-ami-2023.*-x86_64"}}, {Name: aws.String("state"), Values: []string{"available"}}}})
	if err != nil {
		return customerServer{}, err
	}
	if len(images.Images) == 0 {
		return customerServer{}, errors.New("no Amazon Linux 2023 AMI found")
	}
	image := images.Images[0]
	for _, candidate := range images.Images {
		if aws.ToString(candidate.CreationDate) > aws.ToString(image.CreationDate) {
			image = candidate
		}
	}
	vpcs, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: []types.Filter{{Name: aws.String("isDefault"), Values: []string{"true"}}}})
	if err != nil || len(vpcs.Vpcs) == 0 {
		return customerServer{}, errors.New("AWS account needs a default VPC")
	}
	vpcID := aws.ToString(vpcs.Vpcs[0].VpcId)
	subnets, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}, {Name: aws.String("state"), Values: []string{"available"}}}})
	if err != nil || len(subnets.Subnets) == 0 {
		return customerServer{}, errors.New("default VPC has no available subnets")
	}
	var keyBytes [8]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		return customerServer{}, err
	}
	suffix := hex.EncodeToString(keyBytes[:])
	keyName := "crelan-" + suffix
	keyPair, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{KeyName: aws.String(keyName), KeyType: types.KeyTypeEd25519, TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeKeyPair, Tags: []types.Tag{{Key: aws.String("crelan:workspace"), Value: aws.String(fmt.Sprint(workspaceID))}}}}})
	if err != nil {
		return customerServer{}, fmt.Errorf("create SSH key: %w", err)
	}
	group, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{GroupName: aws.String("crelan-" + suffix), Description: aws.String("Crelan server " + input.Name), VpcId: aws.String(vpcID), TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeSecurityGroup, Tags: []types.Tag{{Key: aws.String("crelan:workspace"), Value: aws.String(fmt.Sprint(workspaceID))}}}}})
	if err != nil {
		_, _ = client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: aws.String(keyName)})
		return customerServer{}, err
	}
	_, err = client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{GroupId: group.GroupId, IpPermissions: []types.IpPermission{{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22), IpRanges: []types.IpRange{{CidrIp: aws.String(input.SSHSourceCIDR), Description: aws.String("Customer SSH access")}}}, {IpProtocol: aws.String("tcp"), FromPort: aws.Int32(80), ToPort: aws.Int32(80), IpRanges: []types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}}, {IpProtocol: aws.String("tcp"), FromPort: aws.Int32(443), ToPort: aws.Int32(443), IpRanges: []types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}}}})
	if err != nil {
		_, _ = client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: group.GroupId})
		_, _ = client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: aws.String(keyName)})
		return customerServer{}, err
	}
	var agentTokenBytes [32]byte
	if _, err := rand.Read(agentTokenBytes[:]); err != nil {
		return customerServer{}, err
	}
	agentToken := hex.EncodeToString(agentTokenBytes[:])
	apiURL := strings.TrimRight(strings.TrimSpace(getenv("CRELAN_PUBLIC_URL")), "/")
	userData := fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ndnf install -y docker curl\nsystemctl enable --now docker\ncurl -fsSL %q -o /usr/local/bin/crelan-agent\nchmod 0755 /usr/local/bin/crelan-agent\nprintf 'CRELAN_AGENT_URL=%q\\nCRELAN_AGENT_TOKEN=%q\\n' > /etc/crelan-agent.env\ncat > /etc/systemd/system/crelan-agent.service <<'UNIT'\n[Unit]\nDescription=Crelan VM deployment agent\nAfter=docker.service network-online.target\nWants=network-online.target\n[Service]\nEnvironmentFile=/etc/crelan-agent.env\nExecStart=/usr/local/bin/crelan-agent\nRestart=always\nRestartSec=5\n[Install]\nWantedBy=multi-user.target\nUNIT\nsystemctl daemon-reload\nsystemctl enable --now crelan-agent", apiURL+"/downloads/crelan-agent", apiURL, agentToken)
	agentHash := sha256.Sum256([]byte(agentToken))
	created, err := client.RunInstances(ctx, &ec2.RunInstancesInput{ImageId: image.ImageId, InstanceType: types.InstanceType(input.InstanceType), MinCount: aws.Int32(1), MaxCount: aws.Int32(1), KeyName: aws.String(keyName), SecurityGroupIds: []string{aws.ToString(group.GroupId)}, SubnetId: subnets.Subnets[0].SubnetId, UserData: aws.String(base64.StdEncoding.EncodeToString([]byte(userData))), MetadataOptions: &types.InstanceMetadataOptionsRequest{HttpTokens: types.HttpTokensStateRequired, HttpEndpoint: types.InstanceMetadataEndpointStateEnabled}, TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{{Key: aws.String("Name"), Value: aws.String(input.Name)}, {Key: aws.String("crelan:workspace"), Value: aws.String(fmt.Sprint(workspaceID))}}}, {ResourceType: types.ResourceTypeVolume, Tags: []types.Tag{{Key: aws.String("crelan:workspace"), Value: aws.String(fmt.Sprint(workspaceID))}}}}})
	if err != nil {
		_, _ = client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: group.GroupId})
		_, _ = client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: aws.String(keyName)})
		return customerServer{}, err
	}
	instance := created.Instances[0]
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	status := string(instance.State.Name)
	result, err := s.auth.db.ExecContext(ctx, `INSERT INTO servers(workspace_id,name,mode,region,instance_type,instance_id,key_name,security_group_id,role_arn,external_id,agent_token_hash,public_ip,public_dns,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, workspaceID, input.Name, input.Mode, input.Region, input.InstanceType, aws.ToString(instance.InstanceId), keyName, aws.ToString(group.GroupId), input.RoleARN, input.ExternalID, hex.EncodeToString(agentHash[:]), aws.ToString(instance.PublicIpAddress), aws.ToString(instance.PublicDnsName), status, createdAt, createdAt)
	if err != nil {
		_, _ = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{aws.ToString(instance.InstanceId)}})
		return customerServer{}, err
	}
	id, _ := result.LastInsertId()
	return customerServer{ID: id, Name: input.Name, Mode: input.Mode, Region: input.Region, InstanceType: input.InstanceType, InstanceID: aws.ToString(instance.InstanceId), PublicIP: aws.ToString(instance.PublicIpAddress), PublicDNS: aws.ToString(instance.PublicDnsName), SSHPrivateKey: aws.ToString(keyPair.KeyMaterial), Status: status, CreatedAt: createdAt}, nil
}

func netParseCIDR(v string) (string, int, error) {
	ip, network, err := net.ParseCIDR(v)
	if err != nil {
		return "", 0, err
	}
	ones, _ := network.Mask.Size()
	if ip == nil || (ip.To4() != nil && ones < 24) || (ip.To4() == nil && ones < 64) {
		return "", 0, errors.New("SSH source range is too broad")
	}
	return network.String(), ones, nil
}

func (s *serverService) list(w http.ResponseWriter, r *http.Request) {
	p, err := s.deployments.authenticate(r)
	if err != nil {
		writeError(w, 401, "Sign in required")
		return
	}
	rows, err := s.auth.db.QueryContext(r.Context(), `SELECT id,name,mode,region,instance_type,instance_id,public_ip,public_dns,status,status_message,created_at FROM servers WHERE workspace_id=? AND status != 'deleted' ORDER BY id DESC`, p.workspaceID)
	if err != nil {
		writeError(w, 500, "Could not load servers")
		return
	}
	defer rows.Close()
	items := []customerServer{}
	for rows.Next() {
		var item customerServer
		if err := rows.Scan(&item.ID, &item.Name, &item.Mode, &item.Region, &item.InstanceType, &item.InstanceID, &item.PublicIP, &item.PublicDNS, &item.Status, &item.StatusMessage, &item.CreatedAt); err != nil {
			writeError(w, 500, "Could not load servers")
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 200, items)
}

func (s *serverService) changeState(w http.ResponseWriter, r *http.Request, action string) {
	p, err := s.deployments.authenticate(r)
	if err != nil {
		writeError(w, 401, "Sign in required")
		return
	}
	if !p.hasScope("deployments:write") {
		writeError(w, 403, "Workspace role cannot manage servers")
		return
	}
	var server customerServer
	var roleARN, externalID string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT id,name,mode,region,instance_type,instance_id,role_arn,external_id,status,created_at FROM servers WHERE id=? AND workspace_id=?`, r.PathValue("id"), p.workspaceID).Scan(&server.ID, &server.Name, &server.Mode, &server.Region, &server.InstanceType, &server.InstanceID, &roleARN, &externalID, &server.Status, &server.CreatedAt); err != nil {
		writeError(w, 404, "Server not found")
		return
	}
	client, err := s.awsClient(r.Context(), server.Mode, roleARN, externalID, server.Region)
	if err != nil {
		writeError(w, 502, "Could not connect to AWS")
		return
	}
	switch action {
	case "stop":
		_, err = client.StopInstances(r.Context(), &ec2.StopInstancesInput{InstanceIds: []string{server.InstanceID}})
	case "start":
		_, err = client.StartInstances(r.Context(), &ec2.StartInstancesInput{InstanceIds: []string{server.InstanceID}})
	case "delete":
		_, err = client.TerminateInstances(r.Context(), &ec2.TerminateInstancesInput{InstanceIds: []string{server.InstanceID}})
	}
	if err != nil {
		writeError(w, 502, "AWS could not change the server state")
		return
	}
	status := map[string]string{"stop": "stopping", "start": "pending", "delete": "terminating"}[action]
	_, _ = s.auth.db.ExecContext(r.Context(), `UPDATE servers SET status=?,updated_at=? WHERE id=? AND workspace_id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), server.ID, p.workspaceID)
	server.Status = status
	writeJSON(w, 200, server)
}
func (s *serverService) stop(w http.ResponseWriter, r *http.Request)   { s.changeState(w, r, "stop") }
func (s *serverService) start(w http.ResponseWriter, r *http.Request)  { s.changeState(w, r, "start") }
func (s *serverService) delete(w http.ResponseWriter, r *http.Request) { s.changeState(w, r, "delete") }

type serverDeployment struct {
	ID            int64  `json:"id"`
	ServerID      int64  `json:"server_id"`
	Name          string `json:"name"`
	Image         string `json:"image"`
	Port          int    `json:"port"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message,omitempty"`
	URL           string `json:"url,omitempty"`
	Logs          string `json:"logs,omitempty"`
	CreatedAt     string `json:"created_at"`
}

func (s *serverService) deployImage(w http.ResponseWriter, r *http.Request) {
	p, err := s.deployments.authenticate(r)
	if err != nil {
		writeError(w, 401, "Sign in required")
		return
	}
	if !p.hasScope("deployments:write") {
		writeError(w, 403, "Workspace role cannot deploy")
		return
	}
	var input struct {
		Name  string `json:"name"`
		Image string `json:"image"`
		Port  int    `json:"port"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&input); err != nil {
		writeError(w, 400, "Invalid deployment request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Image = strings.TrimSpace(input.Image)
	if !deploymentNamePattern.MatchString(input.Name) || !containerImagePattern.MatchString(input.Image) || input.Port < 1 || input.Port > 65535 || (input.Port < 1024 && input.Port != 80 && input.Port != 443) {
		writeError(w, 400, "Enter a valid deployment name, image, and application port (80, 443, or 1024–65535)")
		return
	}
	var serverID int64
	var serverStatus, region, mode, roleARN, externalID, securityGroupID string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT id,status,region,mode,role_arn,external_id,security_group_id FROM servers WHERE id=? AND workspace_id=? AND status NOT IN ('terminated','deleted','terminating')`, r.PathValue("id"), p.workspaceID).Scan(&serverID, &serverStatus, &region, &mode, &roleARN, &externalID, &securityGroupID); err != nil {
		writeError(w, 404, "Server not found")
		return
	}
	if serverStatus != "running" {
		writeError(w, 409, "Wait for the server agent to connect before deploying")
		return
	}
	var portTaken int
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM server_deployments WHERE server_id=? AND port=? AND status NOT IN ('failed','deleted')`, serverID, input.Port).Scan(&portTaken); err != nil {
		writeError(w, 500, "Could not check application port")
		return
	}
	if portTaken > 0 {
		writeError(w, 409, "This host port is already used by another deployment")
		return
	}
	if input.Port != 80 && input.Port != 443 {
		client, err := s.awsClient(r.Context(), mode, roleARN, externalID, region)
		if err != nil {
			writeError(w, 502, "Could not connect to AWS")
			return
		}
		_, err = client.AuthorizeSecurityGroupIngress(r.Context(), &ec2.AuthorizeSecurityGroupIngressInput{GroupId: aws.String(securityGroupID), IpPermissions: []types.IpPermission{{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(int32(input.Port)), ToPort: aws.Int32(int32(input.Port)), IpRanges: []types.IpRange{{CidrIp: aws.String("0.0.0.0/0"), Description: aws.String("Crelan application port")}}}}})
		if err != nil {
			writeError(w, 502, "AWS could not expose this application port")
			return
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.auth.db.ExecContext(r.Context(), `INSERT INTO server_deployments(server_id,workspace_id,name,image,port,status,created_at,updated_at) VALUES(?,?,?,?,?,'queued',?,?)`, serverID, p.workspaceID, input.Name, input.Image, input.Port, now, now)
	if err != nil {
		writeError(w, 409, "A deployment with this name already exists on this server")
		return
	}
	id, _ := result.LastInsertId()
	writeJSON(w, 201, serverDeployment{ID: id, ServerID: serverID, Name: input.Name, Image: input.Image, Port: input.Port, Status: "queued", CreatedAt: now})
}
func (s *serverService) listDeployments(w http.ResponseWriter, r *http.Request) {
	p, err := s.deployments.authenticate(r)
	if err != nil {
		writeError(w, 401, "Sign in required")
		return
	}
	rows, err := s.auth.db.QueryContext(r.Context(), `SELECT id,server_id,name,image,port,status,status_message,url,logs,created_at FROM server_deployments WHERE server_id=? AND workspace_id=? ORDER BY id DESC`, r.PathValue("id"), p.workspaceID)
	if err != nil {
		writeError(w, 500, "Could not load deployments")
		return
	}
	defer rows.Close()
	items := []serverDeployment{}
	for rows.Next() {
		var x serverDeployment
		if err := rows.Scan(&x.ID, &x.ServerID, &x.Name, &x.Image, &x.Port, &x.Status, &x.StatusMessage, &x.URL, &x.Logs, &x.CreatedAt); err != nil {
			writeError(w, 500, "Could not load deployments")
			return
		}
		items = append(items, x)
	}
	writeJSON(w, 200, items)
}
func (s *serverService) agentServer(r *http.Request) (int64, error) {
	kind, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(kind, "Bearer") || len(token) < 32 {
		return 0, errors.New("invalid agent token")
	}
	hash := sha256.Sum256([]byte(token))
	var id int64
	err := s.auth.db.QueryRowContext(r.Context(), `SELECT id FROM servers WHERE agent_token_hash=? AND status NOT IN ('terminating','terminated','deleted')`, hex.EncodeToString(hash[:])).Scan(&id)
	return id, err
}
func (s *serverService) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := s.agentServer(r)
	if err != nil {
		writeError(w, 401, "Invalid agent credential")
		return
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `UPDATE servers SET status='running',updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	w.WriteHeader(http.StatusNoContent)
}
func (s *serverService) agentNextCommand(w http.ResponseWriter, r *http.Request) {
	serverID, err := s.agentServer(r)
	if err != nil {
		writeError(w, 401, "Invalid agent credential")
		return
	}
	tx, err := s.auth.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "Agent command unavailable")
		return
	}
	defer tx.Rollback()
	var item serverDeployment
	err = tx.QueryRowContext(r.Context(), `SELECT id,server_id,name,image,port,status,created_at FROM server_deployments WHERE server_id=? AND status='queued' ORDER BY id LIMIT 1`, serverID).Scan(&item.ID, &item.ServerID, &item.Name, &item.Image, &item.Port, &item.Status, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, 500, "Agent command unavailable")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE server_deployments SET status='deploying',updated_at=? WHERE id=? AND status='queued'`, time.Now().UTC().Format(time.RFC3339Nano), item.ID); err != nil {
		writeError(w, 500, "Agent command unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "Agent command unavailable")
		return
	}
	writeJSON(w, 200, item)
}
func (s *serverService) agentCommandResult(w http.ResponseWriter, r *http.Request) {
	serverID, err := s.agentServer(r)
	if err != nil {
		writeError(w, 401, "Invalid agent credential")
		return
	}
	var input struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Logs    string `json:"logs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&input); err != nil {
		writeError(w, 400, "Invalid command result")
		return
	}
	if input.Status != "running" && input.Status != "failed" {
		writeError(w, 400, "Invalid deployment status")
		return
	}
	if len(input.Message) > 1000 {
		input.Message = input.Message[:1000]
	}
	if len(input.Logs) > 64*1024 {
		input.Logs = input.Logs[len(input.Logs)-64*1024:]
	}
	var port int
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT port FROM server_deployments WHERE id=? AND server_id=?`, r.PathValue("command_id"), serverID).Scan(&port); err != nil {
		writeError(w, 404, "Command not found")
		return
	}
	var host string
	_ = s.auth.db.QueryRowContext(r.Context(), `SELECT CASE WHEN public_ip!='' THEN public_ip ELSE public_dns END FROM servers WHERE id=?`, serverID).Scan(&host)
	url := ""
	if host != "" && input.Status == "running" {
		url = fmt.Sprintf("http://%s:%d", host, port)
	}
	_, err = s.auth.db.ExecContext(r.Context(), `UPDATE server_deployments SET status=?,status_message=?,url=?,logs=?,updated_at=? WHERE id=? AND server_id=?`, input.Status, input.Message, url, input.Logs, time.Now().UTC().Format(time.RFC3339Nano), r.PathValue("command_id"), serverID)
	if err != nil {
		writeError(w, 500, "Could not save deployment result")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *serverService) downloadAgent(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(getenv("CRELAN_AGENT_BINARY_PATH"))
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="crelan-agent"`)
	http.ServeFile(w, r, path)
}
