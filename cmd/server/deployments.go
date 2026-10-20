package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var deploymentNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var containerImagePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[a-f0-9]{64})?$`)
var (
	environmentNamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	customDomainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
)

var errProvisionerNotConfigured = errors.New("deployment provider is not configured")
var (
	errDeploymentClusterConflict = errors.New("an app with this name already exists on another cluster")
	errWorkspaceAppLimit         = errors.New("workspace has reached its app limit")
	errWorkspaceConcurrencyLimit = errors.New("workspace has reached its active build limit")
	errWorkspaceResourceLimit    = errors.New("requested CPU or memory exceeds this workspace's per-app limit")
)

type DeploymentRequest struct {
	Name                      string `json:"name"`
	Image                     string `json:"image,omitempty"`
	ImagePullSecret           string `json:"image_pull_secret,omitempty"`
	RepositoryURL             string `json:"repository_url"`
	ClusterContext            string `json:"cluster_context,omitempty"`
	Branch                    string `json:"branch,omitempty"`
	Dockerfile                string `json:"dockerfile,omitempty"`
	Port                      int    `json:"port,omitempty"`
	CPUMillicores             int    `json:"cpu_millicores,omitempty"`
	MemoryMB                  int    `json:"memory_mb,omitempty"`
	WorkspaceMaxApps          int    `json:"workspace_max_apps,omitempty"`
	WorkspaceMaxCPUMillicores int    `json:"workspace_max_cpu_millicores,omitempty"`
	WorkspaceMaxMemoryMB      int    `json:"workspace_max_memory_mb,omitempty"`
	GitHubToken               string `json:"-"`
	PrivateRepo               bool   `json:"-"`
}

type ProvisionedDeployment struct {
	Status string
	URL    string
}

type deploymentProgressProvisioner interface {
	DeployWithProgress(context.Context, int64, DeploymentRequest, func(string) error) (ProvisionedDeployment, error)
}

type deploymentRuntime interface {
	ApplicationResources(context.Context, int64, string) (applicationResources, error)
	ApplicationLogs(context.Context, int64, string, int) (string, error)
}

type workspaceResourceReader interface {
	WorkspaceResources(context.Context, int64) (clusterResources, error)
}

type deploymentReadiness interface {
	WaitForWorkspaceApplication(context.Context, int64, string) error
}

type deploymentHealth interface {
	CheckWorkspaceApplication(context.Context, int64, string) (bool, error)
}

type deploymentConfigurator interface {
	ConfigureApplication(context.Context, int64, string, map[string]string, map[string]string) error
}

type customDomainProvisioner interface {
	ConfigureCustomDomain(context.Context, int64, string, string, string) error
	DeleteCustomDomain(context.Context, int64, string, string) error
}

type applicationResources struct {
	Pods       []podResource       `json:"instances"`
	ConfigMaps []configMapResource `json:"configuration"`
	Secrets    []secretResource    `json:"secret_metadata"`
}

type clusterResources struct {
	Namespace  string              `json:"namespace"`
	Pods       []podResource       `json:"pods"`
	ConfigMaps []configMapResource `json:"configmaps"`
	Secrets    []secretResource    `json:"secrets"`
}

type podResource struct {
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	Ready     bool      `json:"ready"`
	Restarts  int32     `json:"restarts"`
	CreatedAt time.Time `json:"created_at"`
}

type configMapResource struct {
	Name      string    `json:"name"`
	Keys      []string  `json:"keys"`
	CreatedAt time.Time `json:"created_at"`
}

type secretResource struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Keys      []string  `json:"keys"`
	CreatedAt time.Time `json:"created_at"`
}

type deploymentRun struct {
	ID          int64      `json:"id"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type DeploymentProvisioner interface {
	Deploy(context.Context, int64, DeploymentRequest) (ProvisionedDeployment, error)
	Delete(context.Context, int64, string) error
}

type deploymentService struct {
	db            *sql.DB
	auth          *authService
	provisioner   DeploymentProvisioner
	runtime       deploymentRuntime
	providerMu    sync.RWMutex
	activeContext string
	providers     map[string]DeploymentProvisioner
	githubApp     *githubAppService
	workerMu      sync.Mutex
	activeRuns    map[int64]context.CancelFunc
	lookupTXT     func(context.Context, string) ([]string, error)
}

type deployment struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	RepositoryURL  string    `json:"repository_url"`
	Image          string    `json:"image,omitempty"`
	ClusterContext string    `json:"cluster_context,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	Dockerfile     string    `json:"dockerfile"`
	Port           int       `json:"port"`
	CPUMillicores  int       `json:"cpu_millicores"`
	MemoryMB       int       `json:"memory_mb"`
	Status         string    `json:"status"`
	StatusMessage  string    `json:"status_message,omitempty"`
	URL            string    `json:"url,omitempty"`
	LatestRunID    int64     `json:"latest_run_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (s *deploymentService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cluster/contexts", s.clusterContexts)
	mux.HandleFunc("POST /api/v1/cluster/connect", s.connectCluster)
	mux.HandleFunc("GET /api/v1/cluster/resources", s.clusterResources)
	mux.HandleFunc("POST /api/v1/deployments", s.create)
	mux.HandleFunc("GET /api/v1/deployments", s.list)
	mux.HandleFunc("GET /api/v1/deployments/{id}", s.get)
	mux.HandleFunc("GET /api/v1/deployments/{id}/history", s.history)
	mux.HandleFunc("GET /api/v1/deployments/{id}/resources", s.resources)
	mux.HandleFunc("GET /api/v1/deployments/{id}/logs", s.logs)
	mux.HandleFunc("POST /api/v1/deployments/{id}/rollback", s.rollback)
	mux.HandleFunc("PUT /api/v1/deployments/{id}/configuration", s.configure)
	mux.HandleFunc("GET /api/v1/deployments/{id}/domains", s.listDomains)
	mux.HandleFunc("POST /api/v1/deployments/{id}/domains", s.createDomain)
	mux.HandleFunc("POST /api/v1/deployments/{id}/domains/{domain_id}/verify", s.verifyDomain)
	mux.HandleFunc("DELETE /api/v1/deployments/{id}/domains/{domain_id}", s.deleteDomain)
	mux.HandleFunc("GET /api/v1/usage", s.usage)
	mux.HandleFunc("GET /api/v1/audit", s.audit)
	mux.HandleFunc("GET /api/v1/workspace/limits", s.limits)
	mux.HandleFunc("DELETE /api/v1/deployments/{id}", s.delete)
	mux.HandleFunc("POST /api/v1/deployments/{id}/cancel", s.cancel)
}

func (s *deploymentService) currentProvisioner() DeploymentProvisioner {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	if s.providers != nil {
		if provider := s.providers[s.activeContext]; provider != nil {
			return provider
		}
	}
	return s.provisioner
}

func (s *deploymentService) currentRuntime() deploymentRuntime {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	return s.runtime
}

func (s *deploymentService) setCluster(provisioner DeploymentProvisioner, contextName string) {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if s.providers == nil {
		s.providers = make(map[string]DeploymentProvisioner)
	}
	s.providers[contextName] = provisioner
	s.provisioner = provisioner
	s.runtime, _ = provisioner.(deploymentRuntime)
	s.activeContext = contextName
}

func (s *deploymentService) provisionerForContext(contextName string) (DeploymentProvisioner, error) {
	s.providerMu.RLock()
	if contextName == "" {
		contextName = s.activeContext
	}
	provider := s.providers[contextName]
	if provider == nil && contextName == s.activeContext {
		provider = s.provisioner
	}
	s.providerMu.RUnlock()
	if provider != nil {
		return provider, nil
	}
	if contextName == "" {
		return nil, errKubernetesNotConfigured
	}
	created, err := newKubernetesProvisionerForContext(contextName)
	if err != nil {
		return nil, err
	}
	if _, err := created.client.Discovery().ServerVersion(); err != nil {
		return nil, err
	}
	s.providerMu.Lock()
	if s.providers == nil {
		s.providers = make(map[string]DeploymentProvisioner)
	}
	selected := DeploymentProvisioner(created)
	if existing := s.providers[contextName]; existing != nil {
		selected = existing
	} else {
		s.providers[contextName] = created
	}
	s.providerMu.Unlock()
	return selected, nil
}

func (s *deploymentService) clusterContexts(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlatformAdmin(w, r) {
		return
	}
	contexts, current, err := loadKubeconfigContexts()
	if err != nil && !errors.Is(err, errKubernetesNotConfigured) {
		log.Printf("load cluster contexts: %v", err)
		writeError(w, http.StatusBadGateway, "Could not read the server kubeconfig")
		return
	}
	if contexts == nil {
		contexts = []string{}
	}
	s.providerMu.RLock()
	active := s.activeContext
	for name := range s.providers {
		found := false
		for _, contextName := range contexts {
			if contextName == name {
				found = true
				break
			}
		}
		if name != "" && !found {
			contexts = append(contexts, name)
		}
	}
	s.providerMu.RUnlock()
	if active == "" {
		active = current
	}
	if active != "" {
		found := false
		for _, contextName := range contexts {
			if contextName == active {
				found = true
				break
			}
		}
		if !found {
			contexts = append(contexts, active)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contexts":   contexts,
		"active":     active,
		"in_cluster": len(contexts) == 0 && s.currentProvisioner() != nil,
	})
}

func (s *deploymentService) connectCluster(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlatformAdmin(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		Context string `json:"context"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Choose an available cluster")
		return
	}
	input.Context = strings.TrimSpace(input.Context)
	if input.Context == "" {
		writeError(w, http.StatusBadRequest, "Choose an available cluster")
		return
	}
	contexts, _, contextsErr := loadKubeconfigContexts()
	available := contextsErr == nil && slices.Contains(contexts, input.Context)
	s.providerMu.RLock()
	_, knownProvider := s.providers[input.Context]
	s.providerMu.RUnlock()
	if !available && !knownProvider {
		writeError(w, http.StatusForbidden, "That cluster is not available to this workspace")
		return
	}
	provisioner, err := s.provisionerForContext(input.Context)
	if err != nil {
		log.Printf("build Kubernetes client for context %q: %v", input.Context, err)
		writeError(w, http.StatusBadRequest, "Could not load that cluster")
		return
	}
	if client, ok := provisioner.(*kubernetesProvisioner); ok {
		if _, err := client.client.Discovery().ServerVersion(); err != nil {
			log.Printf("connect Kubernetes context %q: %v", input.Context, err)
			writeError(w, http.StatusBadGateway, "Could not reach that cluster with the configured credentials")
			return
		}
	}
	s.setCluster(provisioner, input.Context)
	writeJSON(w, http.StatusOK, map[string]string{"context": input.Context, "status": "connected"})
}

func (s *deploymentService) clusterResources(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in to view cluster resources")
		return
	}
	if !s.isPlatformAdmin(principal) {
		writeError(w, http.StatusForbidden, "Platform administrator access required")
		return
	}
	if !principal.hasScope("resources:read") {
		writeError(w, http.StatusForbidden, "Token requires the resources:read scope")
		return
	}
	reader, ok := s.currentProvisioner().(workspaceResourceReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Kubernetes cluster is not configured")
		return
	}
	resources, err := reader.WorkspaceResources(r.Context(), principal.workspaceID)
	if err != nil {
		log.Printf("list workspace cluster resources: %v", err)
		writeError(w, http.StatusBadGateway, "Could not load Kubernetes resources")
		return
	}
	writeJSON(w, http.StatusOK, resources)
}

func (s *deploymentService) isPlatformAdmin(principal requestPrincipal) bool {
	if !principal.isSession || principal.userID < 1 {
		return false
	}
	var email string
	if err := s.db.QueryRow(`SELECT email FROM users WHERE id = ?`, principal.userID).Scan(&email); err != nil {
		return false
	}
	return isPlatformAdminUser(s.db, principal.userID, email)
}

func (s *deploymentService) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) bool {
	userID, err := s.auth.sessionUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return false
	}
	var email string
	if err := s.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return false
	}
	if !isPlatformAdminUser(s.db, userID, email) {
		writeError(w, http.StatusForbidden, "Platform administrator access required")
		return false
	}
	return true
}

type customDomain struct {
	ID         int64      `json:"id"`
	Host       string     `json:"host"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	TXTName    string     `json:"txt_name,omitempty"`
	TXTValue   string     `json:"txt_value,omitempty"`
}

func (s *deploymentService) listDomains(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:read")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, host, verification_token, status, created_at, verified_at
FROM custom_domains WHERE deployment_id = ? AND workspace_id = ?
ORDER BY id DESC`, item.ID, principal.workspaceID)
	if err != nil {
		log.Printf("list custom domains: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load custom domains")
		return
	}
	defer rows.Close()
	domains := make([]customDomain, 0)
	for rows.Next() {
		var domain customDomain
		var token string
		var created string
		var verified sql.NullString
		if err := rows.Scan(&domain.ID, &domain.Host, &token, &domain.Status, &created, &verified); err != nil {
			log.Printf("scan custom domain: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load custom domains")
			return
		}
		domain.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			log.Printf("parse custom domain date: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load custom domains")
			return
		}
		if verified.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, verified.String)
			if parseErr != nil {
				log.Printf("parse custom domain verification date: %v", parseErr)
				writeError(w, http.StatusInternalServerError, "Could not load custom domains")
				return
			}
			domain.VerifiedAt = &value
		}
		if domain.Status == "pending" {
			domain.TXTName = "_crelan-verify." + domain.Host
			domain.TXTValue = token
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate custom domains: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load custom domains")
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

func (s *deploymentService) createDomain(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:write")
	if !ok {
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		Host string `json:"host"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter a valid custom domain")
		return
	}
	input.Host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(input.Host, ".")))
	if !validCustomDomain(input.Host) {
		writeError(w, http.StatusBadRequest, "Enter a valid public DNS name without a scheme or path")
		return
	}
	token, err := randomToken(24)
	if err != nil {
		log.Printf("generate custom domain verification token: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create domain verification")
		return
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(r.Context(), `
INSERT INTO custom_domains (workspace_id, deployment_id, host, verification_token, status, created_at)
VALUES (?, ?, ?, ?, 'pending', ?)`,
		principal.workspaceID, item.ID, input.Host, token, now.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "This domain is already registered")
			return
		}
		log.Printf("save custom domain: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not register custom domain")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		log.Printf("read custom domain ID: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not register custom domain")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "domain.create", input.Host); err != nil {
		log.Printf("audit custom domain creation: %v", err)
	}
	writeJSON(w, http.StatusCreated, customDomain{
		ID: id, Host: input.Host, Status: "pending", CreatedAt: now,
		TXTName: "_crelan-verify." + input.Host, TXTValue: token,
	})
}

func validCustomDomain(host string) bool {
	if len(host) < 4 || len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !customDomainLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func (s *deploymentService) verifyDomain(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:write")
	if !ok {
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	domainID, err := strconv.ParseInt(r.PathValue("domain_id"), 10, 64)
	if err != nil || domainID < 1 {
		writeError(w, http.StatusNotFound, "Custom domain not found")
		return
	}
	var domain customDomain
	var token string
	err = s.db.QueryRowContext(r.Context(), `
SELECT id, host, verification_token, status FROM custom_domains
WHERE id = ? AND deployment_id = ? AND workspace_id = ?`,
		domainID, item.ID, principal.workspaceID).Scan(&domain.ID, &domain.Host, &token, &domain.Status)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Custom domain not found")
		return
	}
	if err != nil {
		log.Printf("load custom domain verification: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not verify custom domain")
		return
	}
	if domain.Status == "active" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
		return
	}
	txtRecords, err := s.lookupDomainTXT(r.Context(), "_crelan-verify."+domain.Host)
	if err != nil {
		writeError(w, http.StatusConflict, "Domain verification TXT record was not found yet")
		return
	}
	verified := false
	for _, record := range txtRecords {
		if record == token {
			verified = true
			break
		}
	}
	if !verified {
		writeError(w, http.StatusConflict, "Domain verification TXT record does not match")
		return
	}
	cluster, clusterErr := s.provisionerForContext(item.ClusterContext)
	provisioner, ok := cluster.(customDomainProvisioner)
	if clusterErr != nil || !ok {
		writeError(w, http.StatusServiceUnavailable, "Custom domain routing is not supported by the deployment provider")
		return
	}
	issuer := strings.TrimSpace(getenv("CRELAN_CERT_MANAGER_ISSUER"))
	if issuer == "" {
		writeError(w, http.StatusServiceUnavailable, "The platform operator has not configured a certificate issuer")
		return
	}
	if err := provisioner.ConfigureCustomDomain(r.Context(), principal.workspaceID, item.Name, domain.Host, issuer); err != nil {
		log.Printf("configure verified domain %q: %v", domain.Host, err)
		writeError(w, http.StatusBadGateway, "Could not configure the custom domain")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(r.Context(), `
UPDATE custom_domains SET status = 'active', verified_at = ?
WHERE id = ? AND workspace_id = ? AND deployment_id = ?`,
		now, domain.ID, principal.workspaceID, item.ID); err != nil {
		log.Printf("mark custom domain verified: %v", err)
		writeError(w, http.StatusInternalServerError, "Domain routing was applied but its status could not be saved")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "domain.verify", domain.Host); err != nil {
		log.Printf("audit custom domain verification: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

func (s *deploymentService) lookupDomainTXT(ctx context.Context, name string) ([]string, error) {
	if s.lookupTXT != nil {
		return s.lookupTXT(ctx, name)
	}
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func (s *deploymentService) deleteDomain(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:write")
	if !ok {
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	domainID, err := strconv.ParseInt(r.PathValue("domain_id"), 10, 64)
	if err != nil || domainID < 1 {
		writeError(w, http.StatusNotFound, "Custom domain not found")
		return
	}
	var host, status string
	err = s.db.QueryRowContext(r.Context(), `
SELECT host, status FROM custom_domains
WHERE id = ? AND deployment_id = ? AND workspace_id = ?`,
		domainID, item.ID, principal.workspaceID).Scan(&host, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Custom domain not found")
		return
	}
	if err != nil {
		log.Printf("read custom domain: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not remove custom domain")
		return
	}
	if status == "active" {
		cluster, clusterErr := s.provisionerForContext(item.ClusterContext)
		provisioner, ok := cluster.(customDomainProvisioner)
		if clusterErr != nil || !ok {
			writeError(w, http.StatusServiceUnavailable, "Custom domain routing is not supported by the deployment provider")
			return
		}
		if err := provisioner.DeleteCustomDomain(r.Context(), principal.workspaceID, item.Name, host); err != nil {
			log.Printf("remove custom domain routing: %v", err)
			writeError(w, http.StatusBadGateway, "Could not remove custom domain routing")
			return
		}
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM custom_domains WHERE id = ? AND workspace_id = ?`, domainID, principal.workspaceID); err != nil {
		log.Printf("delete custom domain record: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not remove custom domain")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "domain.delete", host); err != nil {
		log.Printf("audit custom domain deletion: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *deploymentService) rollback(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:write")
	if !ok {
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		RunID int64 `json:"run_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil || input.RunID < 1 {
		writeError(w, http.StatusBadRequest, "Choose a successful deployment to roll back to")
		return
	}
	var requestJSON string
	err := s.db.QueryRowContext(r.Context(), `
SELECT request_json FROM deployment_runs
WHERE id = ? AND deployment_id = ? AND workspace_id = ? AND status = 'running'`,
		input.RunID, item.ID, principal.workspaceID).Scan(&requestJSON)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Successful deployment run not found")
		return
	}
	if err != nil {
		log.Printf("load rollback run: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load rollback target")
		return
	}
	var request DeploymentRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		log.Printf("decode rollback run: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load rollback target")
		return
	}
	id, runID, now, err := s.queueDeployment(r.Context(), principal.workspaceID, request)
	if err != nil {
		if errors.Is(err, errDeploymentClusterConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, errWorkspaceAppLimit) || errors.Is(err, errWorkspaceConcurrencyLimit) ||
			errors.Is(err, errWorkspaceResourceLimit) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		log.Printf("queue rollback: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not queue rollback")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "deployment.rollback", fmt.Sprintf("deployment=%d run=%d", item.ID, input.RunID)); err != nil {
		log.Printf("audit deployment rollback: %v", err)
	}
	writeJSON(w, http.StatusAccepted, deployment{
		ID: id, Name: request.Name, RepositoryURL: request.RepositoryURL, Image: request.Image, ClusterContext: request.ClusterContext, Branch: request.Branch,
		Dockerfile: request.Dockerfile, Port: request.Port, CPUMillicores: request.CPUMillicores,
		MemoryMB: request.MemoryMB, Status: "queued",
		StatusMessage: "Rollback queued", LatestRunID: runID, CreatedAt: now, UpdatedAt: now,
	})
}

func (s *deploymentService) configure(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:write")
	if !ok {
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner, err := s.provisionerForContext(item.ClusterContext)
	configurator, ok := provisioner.(deploymentConfigurator)
	if err != nil || !ok {
		writeError(w, http.StatusServiceUnavailable, "Application configuration is not supported by the deployment provider")
		return
	}
	var input struct {
		Environment map[string]string `json:"environment"`
		Secrets     map[string]string `json:"secrets"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter valid environment and secret settings")
		return
	}
	if err := validateEnvironment(input.Environment); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateEnvironment(input.Secrets); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := configurator.ConfigureApplication(r.Context(), principal.workspaceID, item.Name, input.Environment, input.Secrets); err != nil {
		log.Printf("configure application for workspace %d: %v", principal.workspaceID, err)
		writeError(w, http.StatusBadGateway, "Could not update application configuration")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "deployment.configure", item.Name); err != nil {
		log.Printf("audit application configuration: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateEnvironment(values map[string]string) error {
	if len(values) > 100 {
		return errors.New("Each environment section can contain at most 100 entries")
	}
	totalBytes := 0
	for key, value := range values {
		if !environmentNamePattern.MatchString(key) {
			return fmt.Errorf("Invalid environment variable name %q", key)
		}
		if len(value) > 16*1024 {
			return fmt.Errorf("Environment value for %q is too large", key)
		}
		totalBytes += len(key) + len(value)
	}
	if totalBytes > 256*1024 {
		return errors.New("Environment configuration is too large")
	}
	return nil
}

func (s *deploymentService) limits(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("deployments:read") {
		writeError(w, http.StatusForbidden, "Token requires the deployments:read scope")
		return
	}
	limits, err := s.loadWorkspaceLimits(r.Context(), principal.workspaceID)
	if err != nil {
		log.Printf("load workspace limits: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load workspace limits")
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (s *deploymentService) usage(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("usage:read") {
		writeError(w, http.StatusForbidden, "Token requires the usage:read scope")
		return
	}
	var apps, runs, buildSeconds int64
	err = s.db.QueryRowContext(r.Context(), `
SELECT
	(SELECT COUNT(*) FROM deployments WHERE workspace_id = ? AND status != 'deleted'),
	(SELECT COUNT(*) FROM deployment_runs WHERE workspace_id = ? AND completed_at IS NOT NULL),
	COALESCE((SELECT SUM(MAX(0, CAST((julianday(completed_at) - julianday(created_at)) * 86400 AS INTEGER)))
		FROM deployment_runs WHERE workspace_id = ? AND completed_at IS NOT NULL), 0)`,
		principal.workspaceID, principal.workspaceID, principal.workspaceID).Scan(&apps, &runs, &buildSeconds)
	if err != nil {
		log.Printf("read workspace usage: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load workspace usage")
		return
	}
	limits, err := s.loadWorkspaceLimits(r.Context(), principal.workspaceID)
	if err != nil {
		log.Printf("load usage limits: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load workspace usage")
		return
	}
	var allocatedCPU, allocatedMemory int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(cpu_millicores),0), COALESCE(SUM(memory_mb),0) FROM deployments WHERE workspace_id = ? AND status != 'deleted'`, principal.workspaceID).Scan(&allocatedCPU, &allocatedMemory); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load workspace usage")
		return
	}
	allocatedCPU *= int64(configuredAppReplicas())
	allocatedMemory *= int64(configuredAppReplicas())
	databaseCount, databaseStorageGi, databaseUsageAvailable := s.databaseUsage(r.Context(), principal.workspaceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"apps": apps, "completed_deployments": runs, "deployment_seconds": buildSeconds,
		"allocated_cpu_millicores": allocatedCPU, "allocated_memory_mb": allocatedMemory,
		"databases": databaseCount, "database_storage_gib": databaseStorageGi, "database_usage_available": databaseUsageAvailable,
		"limits": limits,
	})
}

func (s *deploymentService) databaseUsage(ctx context.Context, workspaceID int64) (int, int64, bool) {
	provider, err := s.provisionerForContext("")
	cluster, ok := provider.(*kubernetesProvisioner)
	if err != nil || !ok || cluster == nil {
		return 0, 0, false
	}
	namespace, err := cluster.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return 0, 0, false
	}
	count := 0
	var storage int64
	for _, engine := range []string{"postgresql", "mysql"} {
		list, err := cluster.dynamicClient.Resource(databaseGVR(engine)).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: databaseWorkspaceLabel + "=" + strconv.FormatInt(workspaceID, 10)})
		if err != nil {
			if isDatabaseOperatorMissing(err) {
				continue
			}
			return 0, 0, false
		}
		for i := range list.Items {
			count++
			if size := databaseResourceStorage(&list.Items[i], engine); size != "" {
				storage += resourceGi(size) * 3 // Each managed database currently requests three PVCs.
			}
		}
	}
	return count, storage, true
}

func (s *deploymentService) audit(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("audit:read") {
		writeError(w, http.StatusForbidden, "Token requires the audit:read scope")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, user_id, action, details, created_at FROM audit_events
WHERE workspace_id = ? ORDER BY id DESC LIMIT 100`, principal.workspaceID)
	if err != nil {
		log.Printf("read workspace audit events: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load audit events")
		return
	}
	defer rows.Close()
	type auditEvent struct {
		ID        int64     `json:"id"`
		UserID    *int64    `json:"user_id,omitempty"`
		Action    string    `json:"action"`
		Details   string    `json:"details,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	events := make([]auditEvent, 0)
	for rows.Next() {
		var event auditEvent
		var userID sql.NullInt64
		var createdAt string
		if err := rows.Scan(&event.ID, &userID, &event.Action, &event.Details, &createdAt); err != nil {
			log.Printf("scan audit event: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load audit events")
			return
		}
		if userID.Valid {
			event.UserID = &userID.Int64
		}
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			log.Printf("parse audit timestamp: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load audit events")
			return
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate audit events: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load audit events")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *deploymentService) cancel(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("deployments:write") {
		writeError(w, http.StatusForbidden, "Token requires the deployments:write scope")
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	id := r.PathValue("id")
	var name, status string
	var runID sql.NullInt64
	err = s.db.QueryRowContext(r.Context(), `SELECT name, status, latest_run_id FROM deployments WHERE id = ? AND workspace_id = ?`, id, principal.workspaceID).Scan(&name, &status, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Deployment not found")
		return
	}
	if err != nil {
		log.Printf("read deployment for cancel: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not cancel deployment")
		return
	}
	if status != "queued" && status != "building" && status != "deploying" {
		writeError(w, http.StatusConflict, "There is no active deployment to cancel")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cancelActive := false
	if runID.Valid {
		s.workerMu.Lock()
		cancel, active := s.activeRuns[runID.Int64]
		s.workerMu.Unlock()
		if active {
			cancel()
			cancelActive = true
		}
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE deployment_runs SET status = 'cancelled', message = 'Cancelled by user', updated_at = ?, completed_at = ? WHERE deployment_id = ? AND workspace_id = ? AND status = 'queued'`, now, now, id, principal.workspaceID); err != nil {
		log.Printf("cancel deployment runs: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not cancel deployment run")
		return
	}
	if !cancelActive {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE deployment_runs SET status = 'cancelled', message = 'Cancelled by user', updated_at = ?, completed_at = ? WHERE id = ? AND status IN ('building','deploying')`, now, now, runID.Int64); err != nil {
			log.Printf("cancel deployment run: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not cancel deployment run")
			return
		}
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE deployments SET status = 'cancelled', status_message = 'Cancelled by user', updated_at = ? WHERE id = ? AND workspace_id = ? AND status IN ('queued','building','deploying')`, now, id, principal.workspaceID); err != nil {
		log.Printf("record deployment cancel: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not update deployment status")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "deployment.cancel", name); err != nil {
		log.Printf("audit deployment cancellation: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *deploymentService) create(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("deployments:write") {
		writeError(w, http.StatusForbidden, "Token requires the deployments:write scope")
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input DeploymentRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter a valid deployment request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Image = strings.TrimSpace(input.Image)
	input.ImagePullSecret = strings.TrimSpace(input.ImagePullSecret)
	input.ClusterContext = strings.TrimSpace(input.ClusterContext)
	input.Branch = strings.TrimSpace(input.Branch)
	input.Dockerfile = strings.TrimSpace(input.Dockerfile)
	if !deploymentNamePattern.MatchString(input.Name) {
		writeError(w, http.StatusBadRequest, "App name must be a lowercase DNS label of 1 to 63 characters")
		return
	}
	if input.Image != "" {
		if len(input.Image) > 255 || !containerImagePattern.MatchString(input.Image) || strings.Contains(input.Image, "..") {
			writeError(w, http.StatusBadRequest, "Enter a valid container image reference, such as ghcr.io/acme/api:1.0")
			return
		}
		input.RepositoryURL = ""
		input.Branch = ""
		input.Dockerfile = ""
		if input.ImagePullSecret != "" && !deploymentNamePattern.MatchString(input.ImagePullSecret) {
			writeError(w, http.StatusBadRequest, "Choose a valid saved registry credential")
			return
		}
	} else if !validGitHubRepository(input.RepositoryURL) {
		writeError(w, http.StatusBadRequest, "Provide either a container image or an HTTPS GitHub repository URL")
		return
	}
	if input.Image == "" && input.ImagePullSecret != "" {
		writeError(w, http.StatusBadRequest, "Registry credentials can only be used with a container image")
		return
	}
	if len(input.Branch) > 255 || strings.ContainsAny(input.Branch, "\x00\r\n") {
		writeError(w, http.StatusBadRequest, "Branch must be 255 characters or fewer")
		return
	}
	if input.Image == "" && input.Dockerfile == "" {
		input.Dockerfile = "Dockerfile"
	}
	if input.Image == "" && !validDockerfilePath(input.Dockerfile) {
		writeError(w, http.StatusBadRequest, "Dockerfile path must be relative and cannot traverse directories")
		return
	}
	if input.Port == 0 {
		input.Port = 8080
	}
	if input.Port < 1 || input.Port > 65535 {
		writeError(w, http.StatusBadRequest, "App port must be between 1 and 65535")
		return
	}
	if input.CPUMillicores == 0 {
		input.CPUMillicores = 1000
	}
	if input.MemoryMB == 0 {
		input.MemoryMB = 512
	}
	if input.CPUMillicores < 100 || input.MemoryMB < 128 {
		writeError(w, http.StatusBadRequest, "App resources must be at least 100m CPU and 128 MB memory")
		return
	}
	if !s.isPlatformAdmin(principal) {
		input.ClusterContext = ""
	}
	if input.ClusterContext == "" {
		s.providerMu.RLock()
		input.ClusterContext = s.activeContext
		s.providerMu.RUnlock()
	}
	provider, err := s.provisionerForContext(input.ClusterContext)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Selected Kubernetes cluster is not connected or unavailable",
			"code":  "provisioner_unavailable",
		})
		return
	}
	if input.ImagePullSecret != "" {
		cluster, ok := provider.(*kubernetesProvisioner)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "Registry credential validation is unavailable for the selected cluster")
			return
		}
		namespace, namespaceErr := cluster.workspaceNamespace(r.Context(), principal.workspaceID)
		if namespaceErr != nil {
			writeError(w, http.StatusConflict, namespaceErr.Error())
			return
		}
		secret, secretErr := cluster.client.CoreV1().Secrets(namespace).Get(r.Context(), input.ImagePullSecret, metav1.GetOptions{})
		if secretErr != nil || secret.Labels["crelan.io/registry-credential"] != "true" || !ownsWorkspace(secret.Labels, principal.workspaceID) {
			writeError(w, http.StatusBadRequest, "Saved registry credential was not found in this workspace")
			return
		}
	}

	id, runID, now, err := s.queueDeployment(r.Context(), principal.workspaceID, input)
	if err != nil {
		if errors.Is(err, errDeploymentClusterConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, errWorkspaceAppLimit) || errors.Is(err, errWorkspaceConcurrencyLimit) ||
			errors.Is(err, errWorkspaceResourceLimit) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		log.Printf("queue deployment: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not queue deployment")
		return
	}
	writeJSON(w, http.StatusAccepted, deployment{
		ID: id, Name: input.Name, RepositoryURL: input.RepositoryURL, Image: input.Image, ClusterContext: input.ClusterContext, Branch: input.Branch,
		Dockerfile: input.Dockerfile, Port: input.Port, Status: "queued",
		CPUMillicores: input.CPUMillicores, MemoryMB: input.MemoryMB,
		StatusMessage: "Waiting for a build worker", LatestRunID: runID, CreatedAt: now, UpdatedAt: now,
	})
}

func (s *deploymentService) queueDeployment(ctx context.Context, workspaceID int64, input DeploymentRequest) (int64, int64, time.Time, error) {
	if input.CPUMillicores == 0 {
		input.CPUMillicores = 1000
	}
	if input.MemoryMB == 0 {
		input.MemoryMB = 512
	}
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("begin deployment transaction: %w", err)
	}
	defer tx.Rollback()

	limits := workspaceLimits{MaxApps: 5, MaxConcurrency: 1, MaxCPUMilli: 2000, MaxMemoryMB: 4096}
	err = tx.QueryRowContext(ctx, `
SELECT max_apps, max_concurrency, max_cpu_millicores, max_memory_mb
FROM workspace_limits WHERE workspace_id = ?`, workspaceID).
		Scan(&limits.MaxApps, &limits.MaxConcurrency, &limits.MaxCPUMilli, &limits.MaxMemoryMB)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, time.Time{}, fmt.Errorf("load workspace limits: %w", err)
	}
	if input.CPUMillicores > limits.MaxCPUMilli || input.MemoryMB > limits.MaxMemoryMB {
		return 0, 0, time.Time{}, errWorkspaceResourceLimit
	}
	input.WorkspaceMaxApps = limits.MaxApps
	input.WorkspaceMaxCPUMillicores = limits.MaxCPUMilli
	input.WorkspaceMaxMemoryMB = limits.MaxMemoryMB
	requestJSON, err := json.Marshal(input)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("encode deployment request: %w", err)
	}
	var existingID int64
	var existingContext string
	err = tx.QueryRowContext(ctx, `
SELECT id, cluster_context FROM deployments
WHERE workspace_id = ? AND name = ? AND status != 'deleted'`,
		workspaceID, input.Name).Scan(&existingID, &existingContext)
	if errors.Is(err, sql.ErrNoRows) {
		var appsCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE workspace_id = ? AND status != 'deleted'`, workspaceID).Scan(&appsCount); err != nil {
			return 0, 0, time.Time{}, fmt.Errorf("count workspace apps: %w", err)
		}
		if appsCount >= limits.MaxApps {
			return 0, 0, time.Time{}, errWorkspaceAppLimit
		}
	} else if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("check existing app: %w", err)
	} else if existingContext != input.ClusterContext {
		return 0, 0, time.Time{}, errDeploymentClusterConflict
	}
	var activeBuilds int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM deployment_runs
WHERE workspace_id = ? AND status IN ('building', 'deploying')`,
		workspaceID).Scan(&activeBuilds); err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("count active workspace builds: %w", err)
	}
	if activeBuilds >= limits.MaxConcurrency {
		return 0, 0, time.Time{}, errWorkspaceConcurrencyLimit
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO deployments (workspace_id, name, repository_url, image, branch, cluster_context, dockerfile, port, cpu_millicores, memory_mb, status, status_message, url, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', 'Waiting for a build worker', NULL, ?, ?)
ON CONFLICT(workspace_id, name) DO UPDATE SET
	repository_url = excluded.repository_url,
	image = excluded.image,
	branch = excluded.branch,
	cluster_context = excluded.cluster_context,
	dockerfile = excluded.dockerfile,
	port = excluded.port,
	cpu_millicores = excluded.cpu_millicores,
	memory_mb = excluded.memory_mb,
	status = 'queued',
	status_message = 'Waiting for a build worker',
	updated_at = excluded.updated_at
RETURNING id`,
		workspaceID, input.Name, input.RepositoryURL, input.Image, input.Branch, input.ClusterContext,
		input.Dockerfile, input.Port, input.CPUMillicores, input.MemoryMB, nowText, nowText).Scan(&id)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("persist deployment: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO deployment_runs (deployment_id, workspace_id, status, message, request_json, created_at, updated_at)
VALUES (?, ?, 'queued', 'Waiting for a build worker', ?, ?, ?)`,
		id, workspaceID, string(requestJSON), nowText, nowText)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("create deployment run: %w", err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("read deployment run identifier: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET latest_run_id = ? WHERE id = ?`, runID, id); err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("link deployment run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("commit deployment queue: %w", err)
	}
	// audit
	if err := s.recordAudit(ctx, workspaceID, 0, "deployment.create", input.Name); err != nil {
		log.Printf("audit deployment creation: %v", err)
	}
	return id, runID, now, nil
}

func (s *deploymentService) runWorkers(ctx context.Context, count int) error {
	if count < 1 {
		return errors.New("deployment worker count must be positive")
	}
	if err := s.recoverInterruptedRuns(ctx); err != nil {
		return err
	}
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.deploymentWorker(ctx)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	return nil
}

func (s *deploymentService) recoverInterruptedRuns(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
UPDATE deployment_runs SET status = 'queued', message = 'Resuming after server restart', updated_at = ?
WHERE status IN ('building', 'deploying')`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("recover interrupted deployment runs: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = 'queued', status_message = 'Resuming after server restart', updated_at = ?
WHERE latest_run_id IN (SELECT id FROM deployment_runs WHERE status = 'queued')`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("recover interrupted deployments: %w", err)
	}
	return nil
}

func (s *deploymentService) deploymentWorker(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := s.processNextRun(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("process deployment run: %v", err)
		}
		if err := s.reconcileDeploying(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("reconcile deployment readiness: %v", err)
		}
		if err := s.reconcileRunning(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("reconcile running application health: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *deploymentService) processNextRun(ctx context.Context) error {
	var runID, deploymentID, workspaceID int64
	var requestJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT r.id, r.deployment_id, r.workspace_id, r.request_json
FROM deployment_runs r
WHERE r.status = 'queued'
AND NOT EXISTS (
	SELECT 1 FROM deployment_runs active
	WHERE active.deployment_id = r.deployment_id
	  AND active.id != r.id
	  AND active.status IN ('building', 'deploying')
)
ORDER BY r.id
LIMIT 1`).Scan(&runID, &deploymentID, &workspaceID, &requestJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find queued deployment: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	claimed, err := s.db.ExecContext(ctx, `
UPDATE deployment_runs SET status = 'building', message = 'Build started', updated_at = ?
WHERE id = ? AND status = 'queued'`, now, runID)
	if err != nil {
		return fmt.Errorf("claim deployment run: %w", err)
	}
	if changed, _ := claimed.RowsAffected(); changed != 1 {
		return nil
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	s.workerMu.Lock()
	if s.activeRuns == nil {
		s.activeRuns = make(map[int64]context.CancelFunc)
	}
	s.activeRuns[runID] = cancelRun
	s.workerMu.Unlock()
	defer func() {
		cancelRun()
		s.workerMu.Lock()
		delete(s.activeRuns, runID)
		s.workerMu.Unlock()
	}()
	var claimedStatus string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM deployment_runs WHERE id = ?`, runID).Scan(&claimedStatus); err != nil {
		return fmt.Errorf("verify claimed deployment run: %w", err)
	}
	if claimedStatus != "building" {
		return nil
	}
	started, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = 'building', status_message = 'Build started', updated_at = ?
WHERE id = ? AND latest_run_id = ? AND status = 'queued'`, now, deploymentID, runID)
	if err != nil {
		return fmt.Errorf("mark deployment building: %w", err)
	}
	if changed, _ := started.RowsAffected(); changed != 1 {
		return nil
	}
	var request DeploymentRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		return s.finishRun(runID, deploymentID, "failed", "Saved deployment request is invalid")
	}
	if s.githubApp != nil && request.Image == "" {
		request.GitHubToken, err = s.githubApp.installationToken(runCtx, workspaceID)
		if err != nil {
			log.Printf("create GitHub installation token for workspace %d: %v", workspaceID, err)
			return s.finishRun(runID, deploymentID, "failed", "Could not access the connected GitHub installation")
		}
	}
	provisioner, err := s.provisionerForContext(request.ClusterContext)
	if err != nil {
		log.Printf("load target cluster %q for deployment run %d: %v", request.ClusterContext, runID, err)
		return s.finishRun(runID, deploymentID, "failed", "The selected cluster is no longer available")
	}
	progress := func(status string) error {
		return s.updateRunProgress(runCtx, runID, deploymentID, status)
	}
	var provisioned ProvisionedDeployment
	if provider, ok := provisioner.(deploymentProgressProvisioner); ok {
		provisioned, err = provider.DeployWithProgress(runCtx, workspaceID, request, progress)
	} else {
		provisioned, err = provisioner.Deploy(runCtx, workspaceID, request)
	}
	if err != nil {
		if ctx.Err() != nil {
			return s.requeueInterruptedRun(runID, deploymentID)
		}
		if runCtx.Err() != nil {
			return s.finishRun(runID, deploymentID, "cancelled", "Cancelled by user")
		}
		log.Printf("deployment run %d failed: %v", runID, err)
		if errors.Is(err, errPrivateBuildpacks) {
			return s.finishRun(runID, deploymentID, "failed", "Private repositories without a Dockerfile are not supported by the Buildpacks fallback")
		}
		return s.finishRun(runID, deploymentID, "failed", "Build or rollout failed; check the repository and platform configuration")
	}
	if runCtx.Err() != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if err := provisioner.Delete(cleanupCtx, workspaceID, request.Name); err != nil {
			log.Printf("clean up cancelled application %q: %v", request.Name, err)
		}
		return s.finishRun(runID, deploymentID, "cancelled", "Cancelled by user")
	}
	if provisioned.Status != "deploying" && provisioned.Status != "running" {
		log.Printf("deployment run %d returned invalid status %q", runID, provisioned.Status)
		return s.finishRun(runID, deploymentID, "failed", "Deployment provider returned an invalid status")
	}
	if provisioned.Status == "deploying" {
		if err := s.updateRunProgress(ctx, runID, deploymentID, "deploying"); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE deployments SET url = ? WHERE id = ? AND latest_run_id = ?`,
			provisioned.URL, deploymentID, runID); err != nil {
			return fmt.Errorf("save deployment URL: %w", err)
		}
		return nil
	}
	if err := s.finishRun(runID, deploymentID, "running", "Application is ready"); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE deployments SET url = ? WHERE id = ? AND latest_run_id = ?`,
		provisioned.URL, deploymentID, runID)
	return err
}

func (s *deploymentService) reconcileDeploying(ctx context.Context) error {
	var runID, deploymentID, workspaceID int64
	var name, lastChecked, clusterContext string
	err := s.db.QueryRowContext(ctx, `
SELECT r.id, d.id, r.workspace_id, d.name, d.cluster_context, r.updated_at
FROM deployment_runs r
JOIN deployments d ON d.id = r.deployment_id AND d.latest_run_id = r.id
WHERE r.status = 'deploying' AND r.updated_at < ?
ORDER BY r.id
LIMIT 1`, time.Now().UTC().Add(-5*time.Second).Format(time.RFC3339Nano)).
		Scan(&runID, &deploymentID, &workspaceID, &name, &clusterContext, &lastChecked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find deploying application: %w", err)
	}
	provider, err := s.provisionerForContext(clusterContext)
	if err != nil {
		return nil
	}
	readiness, ok := provider.(deploymentReadiness)
	if !ok {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	claimed, err := s.db.ExecContext(ctx, `UPDATE deployment_runs SET updated_at = ? WHERE id = ? AND status = 'deploying' AND updated_at = ?`, now, runID, lastChecked)
	if err != nil {
		return fmt.Errorf("claim readiness check: %w", err)
	}
	if count, _ := claimed.RowsAffected(); count != 1 {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := readiness.WaitForWorkspaceApplication(checkCtx, workspaceID, name); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
		return s.finishRun(runID, deploymentID, "failed", "Application did not become ready")
	}
	return s.finishRun(runID, deploymentID, "running", "Application is ready")
}

func (s *deploymentService) reconcileRunning(ctx context.Context) error {
	var deploymentID, workspaceID int64
	var name, previousUpdatedAt, clusterContext string
	err := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, name, cluster_context, updated_at
FROM deployments
WHERE status IN ('running', 'degraded') AND updated_at < ?
ORDER BY updated_at
LIMIT 1`, time.Now().UTC().Add(-20*time.Second).Format(time.RFC3339Nano)).
		Scan(&deploymentID, &workspaceID, &name, &clusterContext, &previousUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find running application: %w", err)
	}
	provider, err := s.provisionerForContext(clusterContext)
	if err != nil {
		return nil
	}
	health, ok := provider.(deploymentHealth)
	if !ok {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	claimed, err := s.db.ExecContext(ctx, `
UPDATE deployments SET updated_at = ? WHERE id = ? AND updated_at = ? AND status IN ('running', 'degraded')`,
		now, deploymentID, previousUpdatedAt)
	if err != nil {
		return fmt.Errorf("claim running application health check: %w", err)
	}
	if count, _ := claimed.RowsAffected(); count != 1 {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ready, err := health.CheckWorkspaceApplication(checkCtx, workspaceID, name)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("check application readiness: %w", err)
	}
	status, message := "running", "Application is ready"
	if !ready {
		status, message = "degraded", "Application is not currently ready"
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = ?, status_message = ?, updated_at = ?
WHERE id = ? AND status IN ('running', 'degraded')`, status, message, now, deploymentID); err != nil {
		return fmt.Errorf("update application health: %w", err)
	}
	return nil
}

func (s *deploymentService) updateRunProgress(ctx context.Context, runID, deploymentID int64, status string) error {
	if status != "building" && status != "deploying" {
		return fmt.Errorf("invalid deployment progress status %q", status)
	}
	message := map[string]string{"building": "Building application image", "deploying": "Rolling out application"}[status]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `UPDATE deployment_runs SET status = ?, message = ?, updated_at = ? WHERE id = ?`,
		status, message, now, runID); err != nil {
		return fmt.Errorf("update deployment run progress: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = ?, status_message = ?, updated_at = ?
WHERE id = ? AND latest_run_id = ?`, status, message, now, deploymentID, runID); err != nil {
		return fmt.Errorf("update deployment progress: %w", err)
	}
	return nil
}

func (s *deploymentService) finishRun(runID, deploymentID int64, status, message string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.Exec(`
UPDATE deployment_runs SET status = ?, message = ?, updated_at = ?, completed_at = ?
WHERE id = ? AND status IN ('queued', 'building', 'deploying')`, status, message, now, now, runID)
	if err != nil {
		return fmt.Errorf("finish deployment run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deployment run completion: %w", err)
	}
	if changed == 0 {
		return nil
	}
	if _, err := s.db.Exec(`
UPDATE deployments SET status = ?, status_message = ?, updated_at = ?
WHERE id = ? AND latest_run_id = ?`, status, message, now, deploymentID, runID); err != nil {
		return fmt.Errorf("finish deployment: %w", err)
	}
	var workspaceID int64
	if err := s.db.QueryRow(`SELECT workspace_id FROM deployment_runs WHERE id = ?`, runID).Scan(&workspaceID); err != nil {
		return fmt.Errorf("find deployment workspace for audit: %w", err)
	}
	if err := s.recordAudit(context.Background(), workspaceID, 0, "deployment.run.finish", fmt.Sprintf("run=%d status=%s", runID, status)); err != nil {
		log.Printf("audit deployment run completion: %v", err)
	}
	return nil
}

func (s *deploymentService) requeueInterruptedRun(runID, deploymentID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE deployment_runs SET status = 'queued', message = 'Resuming after server shutdown', updated_at = ? WHERE id = ? AND status IN ('building','deploying')`, now, runID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE deployments SET status = 'queued', status_message = 'Resuming after server shutdown', updated_at = ? WHERE id = ? AND latest_run_id = ? AND status IN ('building','deploying')`, now, deploymentID, runID)
	return err
}

func (s *deploymentService) list(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("deployments:read") {
		writeError(w, http.StatusForbidden, "Token requires the deployments:read scope")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, name, repository_url, image, branch, cluster_context, dockerfile, port, cpu_millicores, memory_mb, status, status_message, latest_run_id, url, created_at, updated_at
FROM deployments WHERE workspace_id = ? ORDER BY created_at DESC, id DESC`, principal.workspaceID)
	if err != nil {
		log.Printf("list deployments: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load deployments")
		return
	}
	defer rows.Close()
	items := make([]deployment, 0)
	for rows.Next() {
		item, err := scanDeployment(rows)
		if err != nil {
			log.Printf("read deployment: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load deployments")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate deployments: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load deployments")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type deploymentScanner interface {
	Scan(...any) error
}

func scanDeployment(row deploymentScanner) (deployment, error) {
	var item deployment
	var createdAt, updatedAt string
	var appURL sql.NullString
	var statusMessage sql.NullString
	var runID sql.NullInt64
	err := row.Scan(&item.ID, &item.Name, &item.RepositoryURL, &item.Image, &item.Branch, &item.ClusterContext,
		&item.Dockerfile, &item.Port, &item.CPUMillicores, &item.MemoryMB, &item.Status,
		&statusMessage, &runID, &appURL, &createdAt, &updatedAt)
	if err != nil {
		return deployment{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return deployment{}, err
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return deployment{}, err
	}
	if statusMessage.Valid {
		item.StatusMessage = statusMessage.String
	}
	if runID.Valid {
		item.LatestRunID = runID.Int64
	}
	if appURL.Valid {
		item.URL = appURL.String
	}
	return item, nil
}

func (s *deploymentService) get(w http.ResponseWriter, r *http.Request) {
	_, item, ok := s.authorizedDeployment(w, r, "deployments:read")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *deploymentService) history(w http.ResponseWriter, r *http.Request) {
	principal, _, ok := s.authorizedDeployment(w, r, "deployments:read")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, status, message, created_at, updated_at, completed_at
FROM deployment_runs WHERE deployment_id = ? AND workspace_id = ? ORDER BY id DESC LIMIT 50`,
		r.PathValue("id"), principal.workspaceID)
	if err != nil {
		log.Printf("list deployment history: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load deployment history")
		return
	}
	defer rows.Close()
	runs := make([]deploymentRun, 0)
	for rows.Next() {
		var run deploymentRun
		var created, updated string
		var completed sql.NullString
		if err := rows.Scan(&run.ID, &run.Status, &run.Message, &created, &updated, &completed); err != nil {
			log.Printf("scan deployment history: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load deployment history")
			return
		}
		run.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load deployment history")
			return
		}
		run.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load deployment history")
			return
		}
		if completed.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, completed.String)
			if parseErr != nil {
				writeError(w, http.StatusInternalServerError, "Could not load deployment history")
				return
			}
			run.CompletedAt = &value
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate deployment history: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load deployment history")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *deploymentService) resources(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "resources:read")
	if !ok {
		return
	}
	provider, providerErr := s.provisionerForContext(item.ClusterContext)
	runtime, _ := provider.(deploymentRuntime)
	if providerErr != nil || runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "Runtime resource access is not configured")
		return
	}
	result, err := runtime.ApplicationResources(r.Context(), principal.workspaceID, item.Name)
	if err != nil {
		log.Printf("load runtime resources for workspace %d: %v", principal.workspaceID, err)
		writeError(w, http.StatusBadGateway, "Could not load runtime resources")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *deploymentService) logs(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "logs:read")
	if !ok {
		return
	}
	provider, providerErr := s.provisionerForContext(item.ClusterContext)
	runtime, _ := provider.(deploymentRuntime)
	if providerErr != nil || runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "Application log access is not configured")
		return
	}
	tail := 200
	if raw := r.URL.Query().Get("tail"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			writeError(w, http.StatusBadRequest, "Log tail must be between 1 and 1000 lines")
			return
		}
		tail = value
	}
	logs, err := runtime.ApplicationLogs(r.Context(), principal.workspaceID, item.Name, tail)
	if err != nil {
		log.Printf("load app logs for workspace %d: %v", principal.workspaceID, err)
		writeError(w, http.StatusBadGateway, "Could not load application logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *deploymentService) authorizedDeployment(w http.ResponseWriter, r *http.Request, scope string) (requestPrincipal, deployment, bool) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return requestPrincipal{}, deployment{}, false
	}
	if !principal.hasScope(scope) {
		writeError(w, http.StatusForbidden, "Token requires the "+scope+" scope")
		return requestPrincipal{}, deployment{}, false
	}
	item, err := s.loadDeployment(r.Context(), r.PathValue("id"), principal.workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Deployment not found")
		return requestPrincipal{}, deployment{}, false
	}
	if err != nil {
		log.Printf("load deployment: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load deployment")
		return requestPrincipal{}, deployment{}, false
	}
	return principal, item, true
}

func (s *deploymentService) loadDeployment(ctx context.Context, id string, workspaceID int64) (deployment, error) {
	return scanDeployment(s.db.QueryRowContext(ctx, `
SELECT id, name, repository_url, image, branch, cluster_context, dockerfile, port, cpu_millicores, memory_mb, status, status_message, latest_run_id, url, created_at, updated_at
FROM deployments WHERE id = ? AND workspace_id = ?`, id, workspaceID))
}

// workspaceLimits holds configured workspace guardrails loaded from the database
type workspaceLimits struct {
	MaxApps        int `json:"max_apps"`
	MaxConcurrency int `json:"max_concurrency"`
	MaxCPUMilli    int `json:"max_cpu_millicores"`
	MaxMemoryMB    int `json:"max_memory_mb"`
}

func (s *deploymentService) loadWorkspaceLimits(ctx context.Context, workspaceID int64) (workspaceLimits, error) {
	var l workspaceLimits
	row := s.db.QueryRowContext(ctx, `SELECT max_apps, max_concurrency, max_cpu_millicores, max_memory_mb FROM workspace_limits WHERE workspace_id = ?`, workspaceID)
	if err := row.Scan(&l.MaxApps, &l.MaxConcurrency, &l.MaxCPUMilli, &l.MaxMemoryMB); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// return default conservative limits
			return workspaceLimits{MaxApps: 5, MaxConcurrency: 1, MaxCPUMilli: 2000, MaxMemoryMB: 4096}, nil
		}
		return l, err
	}
	return l, nil
}

func (s *deploymentService) recordAudit(ctx context.Context, workspaceID int64, userID int64, action, details string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var user sql.NullInt64
	if userID > 0 {
		user = sql.NullInt64{Int64: userID, Valid: true}
	} else {
		user = sql.NullInt64{Valid: false}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events (workspace_id, user_id, action, details, created_at) VALUES (?, ?, ?, ?, ?)`, workspaceID, user, action, details, now)
	return err
}

func (s *deploymentService) delete(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return
	}
	if !principal.hasScope("deployments:write") {
		writeError(w, http.StatusForbidden, "Token requires the deployments:write scope")
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	id := r.PathValue("id")
	var name, status, clusterContext string
	err = s.db.QueryRowContext(r.Context(),
		`SELECT name, status, cluster_context FROM deployments WHERE id = ? AND workspace_id = ?`, id, principal.workspaceID).Scan(&name, &status, &clusterContext)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Deployment not found")
		return
	}
	if err != nil {
		log.Printf("read deployment for deletion: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not delete deployment")
		return
	}
	if status == "queued" || status == "building" || status == "deploying" || status == "deleting" {
		writeError(w, http.StatusConflict, "Wait for the active deployment to finish before deleting the application")
		return
	}
	provisioner, err := s.provisionerForContext(clusterContext)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "The deployment's Kubernetes cluster is not connected",
			"code":  "provisioner_unavailable",
		})
		return
	}
	if err := provisioner.Delete(r.Context(), principal.workspaceID, name); err != nil {
		log.Printf("delete deployment for workspace %d: %v", principal.workspaceID, err)
		writeError(w, http.StatusBadGateway, "Deployment provider could not delete this app")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE deployments SET status = 'deleted', status_message = 'Application deleted', updated_at = ? WHERE id = ? AND workspace_id = ?`,
		now, id, principal.workspaceID); err != nil {
		log.Printf("record deployment deletion: %v", err)
		writeError(w, http.StatusInternalServerError, "App was deleted but its record could not be updated")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "deployment.delete", name); err != nil {
		log.Printf("audit deployment deletion: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

type requestPrincipal struct {
	userID        int64
	workspaceID   int64
	workspaceRole string
	scopes        map[string]struct{}
	isSession     bool
}

func (p requestPrincipal) hasScope(scope string) bool {
	if p.isSession {
		switch scope {
		case "deployments:write":
			return p.workspaceRole == "owner" || p.workspaceRole == "admin" || p.workspaceRole == "developer"
		case "tokens:write", "domains:write":
			return p.workspaceRole == "owner" || p.workspaceRole == "admin"
		default:
			return p.workspaceRole == "owner" || p.workspaceRole == "admin" || p.workspaceRole == "developer" || p.workspaceRole == "viewer"
		}
	}
	_, ok := p.scopes[scope]
	return ok
}

func (s *deploymentService) authenticate(r *http.Request) (requestPrincipal, error) {
	header := r.Header.Get("Authorization")
	if header != "" {
		kind, token, ok := strings.Cut(header, " ")
		if !ok || !strings.EqualFold(kind, "Bearer") || token == "" || strings.Contains(token, " ") {
			return requestPrincipal{}, errors.New("invalid authorization header")
		}
		var workspaceID int64
		var scopesJSON string
		var expiresAt, revokedAt sql.NullString
		err := s.db.QueryRowContext(r.Context(), `
SELECT workspace_id, scopes, expires_at, revoked_at FROM api_tokens
WHERE token_hash = ?`, hashToken(token)).Scan(&workspaceID, &scopesJSON, &expiresAt, &revokedAt)
		if errors.Is(err, sql.ErrNoRows) || revokedAt.Valid {
			return requestPrincipal{}, errors.New("token is invalid")
		}
		if err != nil {
			log.Printf("authenticate API token: %v", err)
			return requestPrincipal{}, fmt.Errorf("authenticate API token: %w", err)
		}
		if expiresAt.Valid {
			expiry, err := time.Parse(time.RFC3339Nano, expiresAt.String)
			if err != nil || !expiry.After(time.Now()) {
				return requestPrincipal{}, errors.New("token is expired")
			}
		}
		var scopesList []string
		if err := json.Unmarshal([]byte(scopesJSON), &scopesList); err != nil {
			return requestPrincipal{}, fmt.Errorf("decode token scopes: %w", err)
		}
		scopes := make(map[string]struct{}, len(scopesList))
		for _, scope := range scopesList {
			scopes[scope] = struct{}{}
		}
		return requestPrincipal{workspaceID: workspaceID, scopes: scopes}, nil
	}
	userID, workspaceID, err := s.authSessionIdentity(r)
	if err != nil {
		if !errors.Is(err, http.ErrNoCookie) && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("authenticate deployment session: %v", err)
		}
		return requestPrincipal{}, err
	}
	var role string
	if err := s.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&role); err != nil {
		return requestPrincipal{}, err
	}
	return requestPrincipal{userID: userID, workspaceID: workspaceID, workspaceRole: role, isSession: true}, nil
}

func (s *deploymentService) authSessionIdentity(r *http.Request) (int64, int64, error) {
	return s.auth.sessionWorkspace(r)
}

func validPublicGitHubRepository(raw string) bool {
	return validGitHubRepository(raw)
}

func validGitHubRepository(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return false
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	return validGitHubPathPart(owner) && validGitHubPathPart(repo)
}

func validGitHubPathPart(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validDockerfilePath(value string) bool {
	if len(value) > 255 || strings.ContainsAny(value, "\x00\r\n") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validDeploymentStatus(status string) bool {
	switch status {
	case "queued", "building", "deploying", "running", "degraded", "failed", "deleting", "deleted", "cancelled":
		return true
	default:
		return false
	}
}
