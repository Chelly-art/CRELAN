package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	assets "crelan"
	"encoding/base64"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// adminService provides platform-only endpoints. Platform access is configured
// explicitly through CRELAN_PLATFORM_ADMIN_EMAILS; workspace membership and
// workspace API tokens never grant platform access.
type adminService struct {
	auth        *authService
	provisioner *kubernetesProvisioner
	deployments *deploymentService
	adminEmails map[string]struct{}
}

func newAdminService(auth *authService, provisioners ...*kubernetesProvisioner) *adminService {
	admins := make(map[string]struct{})
	if os.Getenv("CRELAN_DEMO_LOGIN") == "true" || os.Getenv("CRELAN_PLATFORM_ADMIN_EMAILS") == "" {
		admins["admin@crelan.local"] = struct{}{}
	}
	for _, email := range strings.Split(os.Getenv("CRELAN_PLATFORM_ADMIN_EMAILS"), ",") {
		email = strings.ToLower(strings.TrimSpace(email))
		if email != "" {
			admins[email] = struct{}{}
		}
	}
	var provisioner *kubernetesProvisioner
	if len(provisioners) > 0 {
		provisioner = provisioners[0]
	}
	return &adminService{auth: auth, provisioner: provisioner, adminEmails: admins}
}

func isPlatformAdminUser(db *sql.DB, userID int64, email string) bool {
	if userID < 1 || !isPlatformAdminEmail(email) {
		return false
	}
	if os.Getenv("CRELAN_DEMO_LOGIN") == "true" || os.Getenv("CRELAN_PLATFORM_ADMIN_EMAILS") == "" {
		return true
	}
	var linked int
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM oauth_identities WHERE user_id = ? AND (provider = 'github' OR provider = 'demo'))`, userID).Scan(&linked); err != nil {
		return false
	}
	return linked == 1
}

func (s *adminService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", s.servePage)
	mux.HandleFunc("GET /admin/", s.servePage)
	mux.HandleFunc("GET /admin.html", s.servePage)
	mux.HandleFunc("GET /assets/admin.html", s.servePage)
	mux.HandleFunc("GET /api/v1/admin/overview", s.overview)
	mux.HandleFunc("GET /api/v1/admin/readiness", s.readiness)
	mux.HandleFunc("POST /api/v1/admin/clusters/connect", s.connectCluster)
	mux.HandleFunc("GET /api/v1/admin/clusters/agent-manifest", s.getAgentManifest)
	mux.HandleFunc("POST /api/v1/admin/control-plane-backup", s.downloadControlPlaneBackup)

	// Cluster & Node Management
	mux.HandleFunc("GET /api/v1/admin/cluster/info", s.getClusterInfo)
	mux.HandleFunc("GET /api/v1/admin/nodes", s.getNodes)
	mux.HandleFunc("GET /api/v1/admin/nodes/{name}", s.getNodeDetail)

	// Workload Management
	mux.HandleFunc("GET /api/v1/admin/pods", s.getPods)
	mux.HandleFunc("GET /api/v1/admin/pods/{namespace}/{name}", s.getPodDetail)
	mux.HandleFunc("GET /api/v1/admin/pods/{namespace}/{name}/logs", s.getPodLogs)
	mux.HandleFunc("POST /api/v1/admin/pods/{namespace}/{name}/exec", s.execPod)
	mux.HandleFunc("GET /api/v1/admin/pods/{namespace}/{name}/diagnose", s.diagnosePod)
	mux.HandleFunc("DELETE /api/v1/admin/pods/{namespace}/{name}", s.deletePod)
	mux.HandleFunc("GET /api/v1/admin/deployments", s.getDeployments)
	mux.HandleFunc("GET /api/v1/admin/deployments/{namespace}/{name}", s.getDeploymentDetail)
	mux.HandleFunc("POST /api/v1/admin/deployments/{namespace}/{name}/restart", s.restartDeployment)
	mux.HandleFunc("POST /api/v1/admin/deployments/{namespace}/{name}/scale", s.scaleDeployment)
	mux.HandleFunc("POST /api/v1/admin/deployments/{namespace}/{name}/env", s.patchEnvDeployment)
	mux.HandleFunc("DELETE /api/v1/admin/deployments/{namespace}/{name}", s.deleteDeployment)
	mux.HandleFunc("GET /api/v1/admin/statefulsets", s.getStatefulSets)
	mux.HandleFunc("GET /api/v1/admin/daemonsets", s.getDaemonSets)
	mux.HandleFunc("GET /api/v1/admin/jobs", s.getJobs)
	mux.HandleFunc("GET /api/v1/admin/cronjobs", s.getCronJobs)

	// Networking
	mux.HandleFunc("GET /api/v1/admin/services", s.getServices)
	mux.HandleFunc("GET /api/v1/admin/ingresses", s.getIngresses)
	mux.HandleFunc("GET /api/v1/admin/networkpolicies", s.getNetworkPolicies)

	// Configuration & Storage
	mux.HandleFunc("GET /api/v1/admin/configmaps", s.getConfigMaps)
	mux.HandleFunc("GET /api/v1/admin/configmaps/{namespace}/{name}", s.getConfigMapDetail)
	mux.HandleFunc("GET /api/v1/admin/secrets", s.getSecrets)
	mux.HandleFunc("GET /api/v1/admin/secrets/{namespace}/{name}", s.getSecretDetail)
	mux.HandleFunc("GET /api/v1/admin/pvcs", s.getPVCs)
	mux.HandleFunc("DELETE /api/v1/admin/pvcs/{namespace}/{name}", s.deletePVC)
	mux.HandleFunc("GET /api/v1/admin/pvs", s.getPVs)
	mux.HandleFunc("GET /api/v1/admin/storageclasses", s.getStorageClasses)

	// Namespaces, Events & YAML
	mux.HandleFunc("GET /api/v1/admin/namespaces", s.getNamespaces)
	mux.HandleFunc("POST /api/v1/admin/namespaces", s.createNamespace)
	mux.HandleFunc("DELETE /api/v1/admin/namespaces/{name}", s.deleteNamespace)
	mux.HandleFunc("GET /api/v1/admin/events", s.getEvents)
	mux.HandleFunc("GET /api/v1/admin/resource/yaml", s.getResourceYAML)

	// Platform Intelligence & Diagnostics
	mux.HandleFunc("GET /api/v1/admin/workspaces/mapping", s.getWorkspaceMappings)
	mux.HandleFunc("GET /api/v1/admin/security/audit", s.getSecurityAudit)
	mux.HandleFunc("GET /api/v1/admin/security/rbac", s.getRBACBlastRadius)
	mux.HandleFunc("GET /api/v1/admin/access/grants", s.listAccessGrants)
	mux.HandleFunc("POST /api/v1/admin/access/grants", s.createOrUpdateAccessGrant)
	mux.HandleFunc("DELETE /api/v1/admin/access/grants/{id}", s.deleteAccessGrant)
	mux.HandleFunc("POST /api/v1/admin/access/check", s.checkAccessEndpoint)
	mux.HandleFunc("POST /api/v1/admin/deployments/{namespace}/{name}/harden", s.hardenDeployment)
	mux.HandleFunc("GET /api/v1/admin/ai/rightsizing", s.getResourceRightSizing)
	mux.HandleFunc("POST /api/v1/admin/deployments/{namespace}/{name}/rightsize", s.applyRightSizing)
	mux.HandleFunc("POST /api/v1/admin/ai/copilot", s.aiCopilot)
	mux.HandleFunc("GET /api/v1/admin/databases/topology", s.getDatabaseTopology)
	mux.HandleFunc("GET /api/v1/admin/resources/efficiency", s.getResourceEfficiency)

	// Operational Intelligence, Audit & Incident War Room
	mux.HandleFunc("GET /api/v1/admin/audit-trail", s.getAuditTrail)
	mux.HandleFunc("GET /api/v1/admin/incidents/snapshot", s.getIncidentSnapshot)
	mux.HandleFunc("GET /api/v1/admin/pods/{namespace}/{name}/post-mortem", s.getPodPostMortem)
	mux.HandleFunc("GET /api/v1/admin/cluster/upgrade-radar", s.getUpgradeRadar)
	mux.HandleFunc("GET /api/v1/admin/network/topology", s.getNetworkTopology)
	mux.HandleFunc("GET /api/v1/admin/network/matrix", s.getTenantNetworkMatrix)

	// Day-2 Operational Superpowers (Rollback, TLS Radar, Drain Simulator, Webhooks, Dangling Configs)
	mux.HandleFunc("GET /api/v1/admin/deployments/{namespace}/{name}/revisions", s.getDeploymentRevisions)
	mux.HandleFunc("GET /api/v1/admin/deployments/{namespace}/{name}/revisions/{revision}/diff", s.getDeploymentRevisionDiff)
	mux.HandleFunc("POST /api/v1/admin/deployments/{namespace}/{name}/rollback", s.rollbackDeployment)
	mux.HandleFunc("GET /api/v1/admin/security/certificates", s.getTLSCertificates)
	mux.HandleFunc("GET /api/v1/admin/nodes/{name}/drain-simulation", s.getNodeDrainSimulation)
	mux.HandleFunc("POST /api/v1/admin/nodes/{name}/cordon", s.cordonNode)
	mux.HandleFunc("POST /api/v1/admin/nodes/{name}/uncordon", s.uncordonNode)
	mux.HandleFunc("GET /api/v1/admin/webhooks", s.getWebhooks)
	mux.HandleFunc("POST /api/v1/admin/webhooks", s.createWebhook)
	mux.HandleFunc("DELETE /api/v1/admin/webhooks/{id}", s.deleteWebhook)
	mux.HandleFunc("POST /api/v1/admin/webhooks/{id}/test", s.testWebhook)
	mux.HandleFunc("GET /api/v1/admin/security/dangling-configs", s.getDanglingConfigs)

	// Day-2 Superpowers Round 2 (Ephemeral Debug, Event Storm, Autoscaling, GitOps Drift, DR Radar)
	mux.HandleFunc("POST /api/v1/admin/pods/{namespace}/{name}/debug", s.injectDebugContainer)
	mux.HandleFunc("GET /api/v1/admin/events/storm", s.getEventStorm)
	mux.HandleFunc("GET /api/v1/admin/autoscaling", s.getAutoscaling)
	mux.HandleFunc("GET /api/v1/admin/gitops/drift", s.getGitOpsDrift)
	mux.HandleFunc("POST /api/v1/admin/gitops/reconcile", s.reconcileGitOpsDrift)
	mux.HandleFunc("GET /api/v1/admin/dr/radar", s.getDisasterRecoveryRadar)

	// Enterprise Suite (CVE Image Scanner, Helm Releases, CRD Explorer, RBAC can-i, Cluster Fleet)
	mux.HandleFunc("GET /api/v1/admin/security/image-vulnerabilities", s.getImageVulnerabilities)
	mux.HandleFunc("GET /api/v1/admin/helm/releases", s.getHelmReleases)
	mux.HandleFunc("GET /api/v1/admin/helm/releases/{namespace}/{name}/values", s.getHelmReleaseValues)
	mux.HandleFunc("POST /api/v1/admin/helm/releases/{namespace}/{name}/rollback", s.rollbackHelmRelease)
	mux.HandleFunc("DELETE /api/v1/admin/helm/releases/{namespace}/{name}", s.uninstallHelmRelease)
	mux.HandleFunc("GET /api/v1/admin/crds", s.getCRDs)
	mux.HandleFunc("GET /api/v1/admin/crds/{group}/{version}/{resource}", s.getCustomResourceInstances)
	mux.HandleFunc("POST /api/v1/admin/security/can-i", s.checkCanI)
	mux.HandleFunc("GET /api/v1/admin/clusters/fleet", s.getClusterFleet)
	mux.HandleFunc("POST /api/v1/admin/clusters/switch-context", s.switchClusterContext)
	mux.HandleFunc("POST /api/v1/admin/clusters/disconnect", s.disconnectCluster)
	mux.HandleFunc("GET /api/v1/admin/clusters/customizations", s.getClusterCustomizations)
	mux.HandleFunc("POST /api/v1/admin/clusters/customizations", s.saveClusterCustomization)

	// Site Reliability Engineering (SRE) Operations Suite
	mux.HandleFunc("GET /api/v1/admin/sre/slos", s.getSRESLOs)
	mux.HandleFunc("POST /api/v1/admin/sre/slos", s.createSRESLO)
	mux.HandleFunc("DELETE /api/v1/admin/sre/slos/{id}", s.deleteSRESLO)
	mux.HandleFunc("GET /api/v1/admin/sre/golden-signals", s.getSREGoldenSignals)
	mux.HandleFunc("GET /api/v1/admin/sre/probes/audit", s.getSREProbeAudit)
	mux.HandleFunc("GET /api/v1/admin/sre/resilience/audit", s.getSREResilienceAudit)
	mux.HandleFunc("GET /api/v1/admin/sre/canary", s.getSRECanaries)
	mux.HandleFunc("POST /api/v1/admin/sre/canary/abort", s.abortSRECanary)
	mux.HandleFunc("GET /api/v1/admin/sre/chaos/experiments", s.getSREChaosExperiments)
	mux.HandleFunc("POST /api/v1/admin/sre/chaos/run", s.runSREChaosExperiment)
	mux.HandleFunc("GET /api/v1/admin/sre/scorecards", s.getSREReliabilityScorecards)
}

func (s *adminService) connectCluster(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input struct {
		Method             string `json:"method"`
		Kubeconfig         string `json:"kubeconfig"`
		Context            string `json:"context"`
		APIServerURL       string `json:"api_server_url"`
		BearerToken        string `json:"bearer_token"`
		CACert             string `json:"ca_cert"`
		InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON input (maximum 1 MiB)")
		return
	}

	if input.Method == "token" || (strings.TrimSpace(input.Kubeconfig) == "" && strings.TrimSpace(input.APIServerURL) != "") {
		serverURL := strings.TrimSpace(input.APIServerURL)
		if serverURL == "" {
			writeError(w, http.StatusBadRequest, "Enter Kubernetes API server URL (e.g. https://1.2.3.4:6443)")
			return
		}
		if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
			serverURL = "https://" + serverURL
		}
		token := strings.TrimSpace(input.BearerToken)
		if token == "" {
			writeError(w, http.StatusBadRequest, "Enter a ServiceAccount bearer token")
			return
		}
		clusterName := "crelan-sa-cluster"
		userName := "crelan-sa-user"
		contextName := strings.TrimSpace(input.Context)
		if contextName == "" {
			parsedURL, err := url.Parse(serverURL)
			if err == nil && parsedURL.Host != "" {
				hostClean := strings.ReplaceAll(strings.ReplaceAll(parsedURL.Host, ":", "-"), ".", "-")
				contextName = "cluster-" + hostClean
			} else {
				contextName = fmt.Sprintf("sa-cluster-%d", time.Now().Unix())
			}
		}

		clientCfg := clientcmdapi.NewConfig()
		cl := clientcmdapi.NewCluster()
		cl.Server = serverURL
		cl.InsecureSkipTLSVerify = input.InsecureSkipVerify
		if ca := strings.TrimSpace(input.CACert); ca != "" {
			if strings.Contains(ca, "-----BEGIN CERTIFICATE-----") {
				cl.CertificateAuthorityData = []byte(ca)
			} else if decoded, err := base64.StdEncoding.DecodeString(ca); err == nil && len(decoded) > 0 {
				cl.CertificateAuthorityData = decoded
			} else {
				cl.CertificateAuthorityData = []byte(ca)
			}
		} else if !input.InsecureSkipVerify {
			cl.InsecureSkipTLSVerify = true
		}

		authInfo := clientcmdapi.NewAuthInfo()
		authInfo.Token = token

		kCtx := clientcmdapi.NewContext()
		kCtx.Cluster = clusterName
		kCtx.AuthInfo = userName

		clientCfg.Clusters[clusterName] = cl
		clientCfg.AuthInfos[userName] = authInfo
		clientCfg.Contexts[contextName] = kCtx
		clientCfg.CurrentContext = contextName

		yamlBytes, err := clientcmd.Write(*clientCfg)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not generate kubeconfig from ServiceAccount token: "+err.Error())
			return
		}
		input.Kubeconfig = string(yamlBytes)
		input.Context = contextName
	}

	if strings.TrimSpace(input.Kubeconfig) == "" {
		writeError(w, http.StatusBadRequest, "Provide a kubeconfig or ServiceAccount token to connect a cluster")
		return
	}
	config, err := clientcmd.Load([]byte(input.Kubeconfig))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not parse that kubeconfig")
		return
	}
	contextName := strings.TrimSpace(input.Context)
	sourceContext := config.CurrentContext
	if sourceContext == "" && len(config.Contexts) == 1 {
		for name := range config.Contexts {
			sourceContext = name
		}
	}
	if sourceContext == "" {
		for name := range config.Contexts {
			sourceContext = name
			break
		}
	}
	if sourceContext == "" || config.Contexts[sourceContext] == nil {
		writeError(w, http.StatusBadRequest, "No valid context found in the pasted kubeconfig")
		return
	}

	if contextName != "" && config.Contexts[contextName] == nil {
		// User specified a custom cluster context name: clone source context
		config.Contexts[contextName] = config.Contexts[sourceContext].DeepCopy()
		config.CurrentContext = contextName
	} else if contextName == "" {
		contextName = sourceContext
		// If this context is already connected in the multi-cluster fleet, auto-disambiguate
		if s.deployments != nil {
			known := s.deployments.allKnownContexts()
			if slices.Contains(known, contextName) {
				var newServer string
				if clRef := config.Contexts[sourceContext].Cluster; clRef != "" && config.Clusters[clRef] != nil {
					newServer = config.Clusters[clRef].Server
				}
				cleanHost := ""
				if u, err := url.Parse(newServer); err == nil && u.Host != "" {
					cleanHost = strings.ReplaceAll(strings.ReplaceAll(u.Host, ":", "-"), ".", "-")
				}
				candidate := ""
				if cleanHost != "" {
					candidate = fmt.Sprintf("%s-%s", sourceContext, cleanHost)
				} else {
					candidate = fmt.Sprintf("%s-%d", sourceContext, len(known)+1)
				}
				if !slices.Contains(known, candidate) {
					contextName = candidate
					config.Contexts[contextName] = config.Contexts[sourceContext].DeepCopy()
					config.CurrentContext = contextName
				}
			}
		}
	}

	// Update stored kubeconfig YAML to reflect assigned contextName
	if updatedYAML, err := clientcmd.Write(*config); err == nil {
		input.Kubeconfig = string(updatedYAML)
	}
	for _, cluster := range config.Clusters {
		if cluster.CertificateAuthority != "" && len(cluster.CertificateAuthorityData) == 0 {
			if data, err := os.ReadFile(cluster.CertificateAuthority); err == nil && len(data) > 0 {
				cluster.CertificateAuthorityData = data
				cluster.CertificateAuthority = ""
			} else {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Kubeconfig references local file %q which cannot be found on this server. Please flatten your kubeconfig before pasting: run 'kubectl config view --raw --flatten'", cluster.CertificateAuthority))
				return
			}
		}
	}
	for name, authInfo := range config.AuthInfos {
		if authInfo.ClientCertificate != "" && len(authInfo.ClientCertificateData) == 0 {
			if data, err := os.ReadFile(authInfo.ClientCertificate); err == nil && len(data) > 0 {
				authInfo.ClientCertificateData = data
				authInfo.ClientCertificate = ""
			} else {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Kubeconfig user %q references local certificate file %q which cannot be found on this server. Please flatten your kubeconfig before pasting: run 'kubectl config view --raw --flatten'", name, authInfo.ClientCertificate))
				return
			}
		}
		if authInfo.ClientKey != "" && len(authInfo.ClientKeyData) == 0 {
			if data, err := os.ReadFile(authInfo.ClientKey); err == nil && len(data) > 0 {
				authInfo.ClientKeyData = data
				authInfo.ClientKey = ""
			} else {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Kubeconfig user %q references local key file %q which cannot be found on this server. Please flatten your kubeconfig before pasting: run 'kubectl config view --raw --flatten'", name, authInfo.ClientKey))
				return
			}
		}
	}
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*config, contextName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		log.Printf("build Kubernetes client config for context %q: %v", contextName, err)
		writeError(w, http.StatusBadRequest, "Could not load credentials for that context: "+err.Error())
		return
	}
	restConfig.Timeout = 8 * time.Second
	provisioner, err := newKubernetesProvisionerWithRESTConfig(restConfig)
	if err != nil {
		log.Printf("initialize Kubernetes client for context %q: %v", contextName, err)
		writeError(w, http.StatusBadRequest, "Could not initialize client: "+err.Error())
		return
	}
	serverURL := restConfig.Host
	if _, err := provisioner.client.Discovery().ServerVersion(); err != nil {
		log.Printf("connect Kubernetes context %q (%s): %v", contextName, serverURL, err)
		hint := ""
		if strings.Contains(serverURL, "127.0.0.1") || strings.Contains(serverURL, "localhost") {
			hint = " Tip: The cluster API URL is localhost (" + serverURL + "). If Crelan is running in a container, 127.0.0.1 cannot reach your host machine; use host.docker.internal or your workstation's LAN IP."
		}
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Could not reach Kubernetes API at %s within 8 seconds (%v).%s", serverURL, err, hint))
		return
	}
	if s.deployments == nil {
		writeError(w, http.StatusServiceUnavailable, "Deployment service is not available")
		return
	}
	persisted := true
	persistWarning := ""
	if s.auth.databasePath == ":memory:" {
		persisted = false
		persistWarning = "Connected in-memory for this session (set DATABASE_PATH to persist across server restarts)"
	} else if err := saveKubeconfigCredential(s.auth.db, contextName, input.Kubeconfig); err != nil {
		persisted = false
		if errors.Is(err, errKubeconfigEncryptionKeyMissing) || strings.Contains(err.Error(), "CRELAN_KUBECONFIG_ENCRYPTION_KEY") {
			persistWarning = "Connected in-memory for this session (set CRELAN_KUBECONFIG_ENCRYPTION_KEY to persist across server restarts)"
		} else {
			log.Printf("save encrypted kubeconfig for context %q: %v", contextName, err)
			persistWarning = "Connected in-memory for this session (could not encrypt to database: " + err.Error() + ")"
		}
	}
	s.deployments.setCluster(provisioner, contextName)
	s.provisioner = provisioner
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "connect", "Cluster", "", contextName, "Connected cluster context: "+contextName)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"context":         contextName,
		"status":          "connected",
		"persisted":       persisted,
		"persist_warning": persistWarning,
	})
}

func (s *adminService) disconnectCluster(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		Context string `json:"context"`
	}
	if err := decodeJSON(w, r, &input); err != nil || strings.TrimSpace(input.Context) == "" {
		writeError(w, http.StatusBadRequest, "Cluster context name is required")
		return
	}
	contextName := strings.TrimSpace(input.Context)
	if s.auth.db != nil {
		_ = deleteKubeconfigCredential(s.auth.db, contextName)
	}
	if s.deployments != nil {
		s.deployments.removeClusterProvider(contextName)
	}
	s.provisioner = s.currentKubernetesProvisioner()

	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "disconnect", "Cluster", "", contextName, "Disconnected cluster context: "+contextName)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "disconnected",
		"context": contextName,
		"message": fmt.Sprintf("Cluster context %s has been disconnected", contextName),
	})
}

func (s *adminService) getAgentManifest(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	_, workspaceID, _ := s.auth.sessionWorkspace(r)

	host := r.Host
	scheme := "https"
	if r.TLS == nil && (strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")) {
		scheme = "http"
	}
	serverURL := fmt.Sprintf("%s://%s", scheme, host)
	clusterToken := fmt.Sprintf("cr-agent-%d-%d", workspaceID, time.Now().Unix())

	manifestYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: crelan-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: crelan-agent
  namespace: crelan-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: crelan-agent-role
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: crelan-agent-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: crelan-agent-role
subjects:
  - kind: ServiceAccount
    name: crelan-agent
    namespace: crelan-system
---
apiVersion: v1
kind: Secret
metadata:
  name: crelan-agent-config
  namespace: crelan-system
type: Opaque
stringData:
  SERVER_URL: "%s"
  CLUSTER_TOKEN: "%s"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: crelan-agent
  namespace: crelan-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: crelan-agent
  template:
    metadata:
      labels:
        app: crelan-agent
    spec:
      serviceAccountName: crelan-agent
      containers:
        - name: agent
          image: curlimages/curl:latest
          command:
            - /bin/sh
            - -c
            - |
              echo "Starting Crelan Outbound Agent Tunnel..."
              while true; do
                TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
                curl -s -k -X POST "$SERVER_URL/api/v1/admin/clusters/connect" \
                  -H "Content-Type: application/json" \
                  -d "{\"method\":\"token\",\"api_server_url\":\"https://kubernetes.default.svc\",\"bearer_token\":\"$TOKEN\",\"insecure_skip_verify\":true,\"context\":\"crelan-agent-cluster\"}" || true
                sleep 60
              done
          envFrom:
            - secretRef:
                name: crelan-agent-config
`, serverURL, clusterToken)

	installCommand := fmt.Sprintf("curl -sSL '%s/api/v1/admin/clusters/agent-manifest?format=yaml' | kubectl apply -f -", serverURL)

	if r.URL.Query().Get("format") == "yaml" || strings.Contains(r.Header.Get("Accept"), "yaml") {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(manifestYAML))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"server_url":      serverURL,
		"cluster_token":   clusterToken,
		"manifest_yaml":   manifestYAML,
		"install_command": installCommand,
	})
}

func (s *adminService) currentKubernetesProvisioner() *kubernetesProvisioner {
	if s.deployments != nil {
		if provisioner, ok := s.deployments.currentProvisioner().(*kubernetesProvisioner); ok {
			return provisioner
		}
		return nil
	}
	return s.provisioner
}

func (s *adminService) servePage(w http.ResponseWriter, r *http.Request) {
	userID, err := s.auth.sessionUserID(r)
	if err != nil {
		http.Redirect(w, r, "/login?redirect=/admin", http.StatusSeeOther)
		return
	}
	var email string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.adminEmails[strings.ToLower(strings.TrimSpace(email))]; !ok || !isPlatformAdminUser(s.auth.db, userID, email) {
		http.NotFound(w, r)
		return
	}
	page, err := assets.Web.ReadFile("web/admin.html")
	if err != nil {
		log.Printf("read platform admin page: %v", err)
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (s *adminService) downloadControlPlaneBackup(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	if s.auth.databasePath == ":memory:" {
		writeError(w, http.StatusConflict, "A file-backed DATABASE_PATH is required for a control-plane backup")
		return
	}
	directory, err := os.MkdirTemp("", "crelan-control-plane-backup-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not prepare a control-plane backup")
		return
	}
	defer os.RemoveAll(directory)
	backupPath := filepath.Join(directory, "crelan-control-plane.db")
	if _, err := s.auth.db.ExecContext(r.Context(), `VACUUM INTO ?`, backupPath); err != nil {
		log.Printf("create control-plane SQLite backup: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create a control-plane backup")
		return
	}
	file, err := os.Open(backupPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the control-plane backup")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="crelan-control-plane.db"`)
	http.ServeContent(w, r, "crelan-control-plane.db", time.Now(), file)
}

type readinessCheck struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail"`
}

func (s *adminService) readiness(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	checks := s.clusterPreflightChecks(r.Context(), r.URL.Query().Get("workspace_id"))
	ready := true
	for _, check := range checks {
		if !check.Ready {
			ready = false
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready": ready, "checks": checks,
		"required_rbac": preflightRequiredRBAC,
	})
}

func (s *adminService) authorize(w http.ResponseWriter, r *http.Request) bool {
	// Platform actions require a browser session. Workspace-scoped bearer tokens
	// deliberately cannot be used as platform credentials.
	userID, err := s.auth.sessionUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return false
	}
	var email string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("load platform admin identity: %v", err)
		}
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return false
	}
	if _, ok := s.adminEmails[strings.ToLower(strings.TrimSpace(email))]; !ok || !isPlatformAdminUser(s.auth.db, userID, email) {
		writeError(w, http.StatusForbidden, "Platform administrator access required")
		return false
	}
	return true
}

func (s *adminService) currentOperatorEmail(r *http.Request) string {
	userID, err := s.auth.sessionUserID(r)
	if err != nil {
		return "platform-admin"
	}
	var email string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		return "platform-admin"
	}
	return email
}

type platformOverview struct {
	Users       int64            `json:"users"`
	Workspaces  int64            `json:"workspaces"`
	Deployments int64            `json:"deployments"`
	ByStatus    map[string]int64 `json:"deployments_by_status"`
	Cluster     platformCluster  `json:"cluster"`
}

type platformCluster struct {
	Connected    bool     `json:"connected"`
	Context      string   `json:"context,omitempty"`
	Nodes        int      `json:"nodes"`
	ReadyNodes   int      `json:"ready_nodes"`
	TenantSpaces int      `json:"tenant_namespaces"`
	Namespaces   []string `json:"namespaces,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func (s *adminService) overview(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	overview, err := loadPlatformOverview(r.Context(), s.auth.db)
	if err != nil {
		log.Printf("load platform overview: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load platform overview")
		return
	}
	overview.Cluster = s.clusterOverview(r)
	writeJSON(w, http.StatusOK, overview)
}

func (s *adminService) clusterOverview(r *http.Request) platformCluster {
	contextName := ""
	if s.deployments != nil {
		contextName = s.deployments.activeClusterContext()
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil {
		return platformCluster{Context: contextName, Error: "No Kubernetes cluster is configured"}
	}
	ctx := r.Context()
	nodes, err := provisioner.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return platformCluster{Context: contextName, Error: "Could not list cluster nodes; check platform admin RBAC"}
	}
	spaces, err := provisioner.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: capsuleTenantLabel + ""})
	if err != nil {
		return platformCluster{Context: contextName, Nodes: len(nodes.Items), ReadyNodes: countReadyNodes(nodes.Items), Error: "Could not list Capsule namespaces; check platform admin RBAC"}
	}
	namespaces := make([]string, 0, len(spaces.Items))
	for _, namespace := range spaces.Items {
		if namespace.Labels[capsuleTenantLabel] != "" {
			namespaces = append(namespaces, namespace.Name)
		}
	}
	return platformCluster{Connected: true, Context: contextName, Nodes: len(nodes.Items), ReadyNodes: countReadyNodes(nodes.Items), TenantSpaces: len(namespaces), Namespaces: namespaces}
}

func countReadyNodes(nodes []corev1.Node) int {
	ready := 0
	for _, node := range nodes {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}
	return ready
}

func loadPlatformOverview(ctx context.Context, db *sql.DB) (platformOverview, error) {
	var overview platformOverview
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&overview.Users); err != nil {
		return platformOverview{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&overview.Workspaces); err != nil {
		return platformOverview{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments`).Scan(&overview.Deployments); err != nil {
		return platformOverview{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(*) FROM deployments GROUP BY status`)
	if err != nil {
		return platformOverview{}, err
	}
	defer rows.Close()
	overview.ByStatus = make(map[string]int64)
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return platformOverview{}, err
		}
		overview.ByStatus[status] = count
	}
	if err := rows.Err(); err != nil {
		return platformOverview{}, err
	}
	return overview, nil
}

// -------------------------------------------------------------
// Cluster & Resource API Handlers
// -------------------------------------------------------------

func (s *adminService) getClusterInfo(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	contextName := ""
	if s.deployments != nil {
		contextName = s.deployments.activeClusterContext()
	}
	provisioner := s.currentKubernetesProvisioner()
	var client kubernetes.Interface
	if provisioner != nil {
		client = provisioner.client
	}
	info, err := fetchClusterInfo(r.Context(), client, contextName, s.auth.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch cluster info: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *adminService) getNodes(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	nodes, err := fetchNodesList(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list cluster nodes: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *adminService) getNodeDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	node, err := provisioner.client.CoreV1().Nodes().Get(r.Context(), r.PathValue("name"), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *adminService) getPods(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, wsMap, _ := fetchWorkspaceMappings(ctx, s.auth.db)
	namespace := r.URL.Query().Get("namespace")
	pods, err := fetchPodsList(ctx, provisioner.client, namespace, wsMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list pods: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pods)
}

func (s *adminService) getPodDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	pod, err := provisioner.client.CoreV1().Pods(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Pod not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pod)
}

func (s *adminService) getPodLogs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	container := r.URL.Query().Get("container")
	tailStr := r.URL.Query().Get("tail")
	previous := r.URL.Query().Get("previous") == "true"

	tailLines := int64(250)
	if tailStr != "" {
		if t, err := strconv.ParseInt(tailStr, 10, 64); err == nil && t > 0 {
			tailLines = t
		}
	}

	logs, err := fetchPodLogs(r.Context(), provisioner.client, ns, name, container, tailLines, previous)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not retrieve pod logs: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(logs))
}

func (s *adminService) execPod(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	var input struct {
		Command   string `json:"command"`
		Container string `json:"container"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid exec request")
		return
	}
	if strings.TrimSpace(input.Command) == "" {
		input.Command = "uptime"
	}

	stdout, stderr, err := execPodCommand(r.Context(), provisioner, ns, name, input.Container, input.Command)
	res := map[string]any{
		"stdout": stdout,
		"stderr": stderr,
		"error":  "",
	}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *adminService) diagnosePod(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	report, err := runAIDiagnosis(r.Context(), provisioner.client, ns, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not run AI diagnosis: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) deletePod(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	force := r.URL.Query().Get("force") == "true"

	var err error
	if force {
		err = forceDeletePod(r.Context(), provisioner.client, ns, name)
	} else {
		err = provisioner.client.CoreV1().Pods(ns).Delete(r.Context(), name, metav1.DeleteOptions{})
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete pod: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "delete", "Pod", ns, name, fmt.Sprintf("Pod evicted and deleted (force=%v)", force))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "pod": name})
}

func (s *adminService) getDeployments(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	_, wsMap, _ := fetchWorkspaceMappings(r.Context(), s.auth.db)
	ns := r.URL.Query().Get("namespace")
	deps, err := fetchDeploymentsList(r.Context(), provisioner.client, ns, wsMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list deployments: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, deps)
}

func (s *adminService) getDeploymentDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	dep, err := provisioner.client.AppsV1().Deployments(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Deployment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

func (s *adminService) restartDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	if err := rolloutRestartDeployment(r.Context(), provisioner.client, ns, name); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not restart deployment: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "restart", "Deployment", ns, name, "Rollout restart triggered")
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "deployment": name})
}

func (s *adminService) scaleDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	var input struct {
		Replicas int32 `json:"replicas"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid replica count")
		return
	}
	if err := scaleDeploymentReplicas(r.Context(), provisioner.client, ns, name, input.Replicas); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not scale deployment: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "scale", "Deployment", ns, name, fmt.Sprintf("Scaled replicas to %d", input.Replicas))
	writeJSON(w, http.StatusOK, map[string]any{"status": "scaled", "deployment": name, "replicas": input.Replicas})
}

func (s *adminService) patchEnvDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	var input struct {
		Env map[string]string `json:"env"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid env variables")
		return
	}
	if err := patchDeploymentEnv(r.Context(), provisioner.client, ns, name, input.Env); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update environment variables: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "patch-env", "Deployment", ns, name, fmt.Sprintf("Updated %d environment variables", len(input.Env)))
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "deployment": name})
}

func (s *adminService) deleteDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	if err := provisioner.client.AppsV1().Deployments(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete deployment: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "delete", "Deployment", ns, name, "Deployment deleted")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "deployment": name})
}

func (s *adminService) getStatefulSets(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchStatefulSetsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getDaemonSets(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchDaemonSetsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getJobs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchJobsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getCronJobs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchCronJobsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getServices(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchServicesList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getIngresses(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchIngressesList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getNetworkPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchNetworkPoliciesList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getConfigMaps(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchConfigMapsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getConfigMapDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	cm, err := provisioner.client.CoreV1().ConfigMaps(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "ConfigMap not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cm)
}

func (s *adminService) getSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchSecretsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getSecretDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	sec, err := provisioner.client.CoreV1().Secrets(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Secret not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	decodedData := make(map[string]string)
	for k, v := range sec.Data {
		decodedData[k] = string(v)
	}

	res := map[string]any{
		"name":         sec.Name,
		"namespace":    sec.Namespace,
		"type":         string(sec.Type),
		"labels":       sec.Labels,
		"annotations":  sec.Annotations,
		"created_at":   sec.CreationTimestamp.UTC().Format(time.RFC3339),
		"data":         decodedData,
		"keys":         len(decodedData),
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *adminService) getPVCs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := fetchPVCsList(r.Context(), provisioner.client, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) deletePVC(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	if err := provisioner.client.CoreV1().PersistentVolumeClaims(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete PVC: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "pvc": name})
}

func (s *adminService) getPVs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	list, err := fetchPVsList(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getStorageClasses(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	list, err := fetchStorageClassesList(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getNamespaces(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := fetchNamespacesList(ctx, provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) createNamespace(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}

	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil || strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "Namespace name is required")
		return
	}
	nsName := strings.ToLower(strings.TrimSpace(input.Name))

	created, err := provisioner.client.CoreV1().Namespaces().Create(r.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create namespace: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "create", "Namespace", "", created.Name, "Namespace created")
	writeJSON(w, http.StatusCreated, map[string]string{"name": created.Name, "status": string(created.Status.Phase)})
}

func (s *adminService) deleteNamespace(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	name := r.PathValue("name")
	if name == "default" || name == "kube-system" || name == "kube-public" {
		writeError(w, http.StatusForbidden, "System namespaces cannot be deleted")
		return
	}
	if err := provisioner.client.CoreV1().Namespaces().Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete namespace: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "delete", "Namespace", "", name, "Namespace deleted")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleting", "namespace": name})
}

func (s *adminService) getEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	evType := r.URL.Query().Get("type")
	list, err := fetchEventsList(r.Context(), provisioner.client, ns, evType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *adminService) getResourceYAML(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	kind := strings.ToLower(r.URL.Query().Get("kind"))
	ns := r.URL.Query().Get("namespace")
	name := r.URL.Query().Get("name")

	ctx := r.Context()
	var rawObj any
	var err error

	switch kind {
	case "pod", "pods":
		rawObj, err = provisioner.client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	case "deployment", "deployments":
		rawObj, err = provisioner.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	case "statefulset", "statefulsets", "sts":
		rawObj, err = provisioner.client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	case "daemonset", "daemonsets", "ds":
		rawObj, err = provisioner.client.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	case "job", "jobs":
		rawObj, err = provisioner.client.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
	case "cronjob", "cronjobs", "cj":
		rawObj, err = provisioner.client.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	case "service", "services":
		rawObj, err = provisioner.client.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	case "configmap", "configmaps":
		rawObj, err = provisioner.client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	case "secret", "secrets":
		rawObj, err = provisioner.client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	case "node", "nodes":
		rawObj, err = provisioner.client.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	case "persistentvolumeclaim", "pvc", "pvcs":
		rawObj, err = provisioner.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
	case "persistentvolume", "pv", "pvs":
		rawObj, err = provisioner.client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	case "storageclass", "storageclasses", "sc":
		rawObj, err = provisioner.client.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	case "ingress", "ingresses":
		rawObj, err = provisioner.client.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
	case "networkpolicy", "networkpolicies", "netpol":
		rawObj, err = provisioner.client.NetworkingV1().NetworkPolicies(ns).Get(ctx, name, metav1.GetOptions{})
	case "namespace", "namespaces", "ns":
		rawObj, err = provisioner.client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	default:
		writeError(w, http.StatusBadRequest, "Unsupported resource kind for YAML inspection")
		return
	}

	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Resource not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rawObj)
}

// -------------------------------------------------------------
// Platform Intelligence & Diagnostics Handlers
// -------------------------------------------------------------

func (s *adminService) getWorkspaceMappings(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	mappings, _, err := fetchWorkspaceMappings(r.Context(), s.auth.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch workspace mappings: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mappings)
}

func (s *adminService) getSecurityAudit(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	audit, err := runSecurityAudit(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not run security audit: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, audit)
}

func (s *adminService) getDatabaseTopology(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	topo, err := fetchManagedDatabasesTopology(r.Context(), provisioner.client, provisioner.dynamicClient)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch database topology: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, topo)
}

func (s *adminService) getResourceEfficiency(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	eff, err := calculateResourceEfficiency(r.Context(), provisioner.client, s.auth.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not calculate resource efficiency: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, eff)
}

func (s *adminService) getRBACBlastRadius(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := runRBACBlastRadiusAnalysis(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not run RBAC analysis: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) hardenDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	if err := hardenDeployment(r.Context(), provisioner.client, ns, name); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not harden deployment: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "harden", "Deployment", ns, name, "Hardened securityContext (non-root, drop ALL caps, RuntimeDefault seccomp)")
	writeJSON(w, http.StatusOK, map[string]any{"status": "hardened", "message": fmt.Sprintf("Deployment %s/%s successfully hardened with non-root, dropped capabilities, and runtime seccomp profile.", ns, name)})
}

func (s *adminService) getResourceRightSizing(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := calculateResourceRightSizing(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not calculate right-sizing: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) applyRightSizing(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	var input rightSizeApplyRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid right-sizing parameters")
		return
	}

	if err := applyRightSizingPatch(r.Context(), provisioner.client, ns, name, input.CPURequest, input.CPULimit, input.MemRequest, input.MemLimit); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not apply right-sizing patch: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "rightsize", "Deployment", ns, name, fmt.Sprintf("Applied right-sizing: CPU %s/%s, Mem %s/%s", input.CPURequest, input.CPULimit, input.MemRequest, input.MemLimit))
	writeJSON(w, http.StatusOK, map[string]any{"status": "applied", "message": fmt.Sprintf("Deployment %s/%s resources successfully updated.", ns, name)})
}

func (s *adminService) aiCopilot(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input aiCopilotRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid copilot query")
		return
	}
	if strings.TrimSpace(input.Query) == "" {
		writeError(w, http.StatusBadRequest, "Query cannot be empty")
		return
	}

	provisioner := s.currentKubernetesProvisioner()
	var client kubernetes.Interface
	if provisioner != nil {
		client = provisioner.client
	}

	resp, err := runAICopilotQuery(r.Context(), client, s.auth.db, input.Query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Copilot query evaluation failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *adminService) getAuditTrail(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	ns := r.URL.Query().Get("namespace")
	actionType := r.URL.Query().Get("action_type")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	logs, err := fetchPlatformAuditLogs(r.Context(), s.auth.db, ns, actionType, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch platform audit trail: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *adminService) getIncidentSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.URL.Query().Get("namespace")
	pod := r.URL.Query().Get("pod")
	snapshot, err := fetchIncidentSnapshot(r.Context(), provisioner.client, ns, pod)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not capture incident snapshot: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *adminService) getPodPostMortem(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	report, err := generatePostMortemReport(r.Context(), provisioner.client, s.auth.db, ns, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not generate post-mortem report: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) getUpgradeRadar(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := runClusterUpgradeRadar(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not run cluster upgrade radar: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) getNetworkTopology(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := fetchNetworkTrafficTopology(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not map network traffic topology: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) getTenantNetworkMatrix(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := calculateTenantNetworkMatrix(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not calculate tenant network isolation matrix: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) getDeploymentRevisions(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	revs, err := fetchDeploymentRevisions(r.Context(), provisioner.client, ns, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch deployment revisions: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, revs)
}

func (s *adminService) getDeploymentRevisionDiff(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	revStr := r.PathValue("revision")
	rev, err := strconv.ParseInt(revStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid revision number")
		return
	}
	diff, err := calculateRevisionDiff(r.Context(), provisioner.client, ns, name, rev)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not calculate revision diff: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *adminService) rollbackDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	var input struct {
		TargetRevision int64 `json:"targetRevision"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid rollback payload")
		return
	}
	if input.TargetRevision <= 0 {
		writeError(w, http.StatusBadRequest, "Target revision must be positive")
		return
	}
	if err := rollbackDeploymentRevision(r.Context(), provisioner.client, ns, name, input.TargetRevision); err != nil {
		writeError(w, http.StatusInternalServerError, "Rollback failed: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "rollback", "Deployment", ns, name, fmt.Sprintf("Rolled back to revision %d", input.TargetRevision))
	writeJSON(w, http.StatusOK, map[string]any{"status": "rolled_back", "deployment": name, "targetRevision": input.TargetRevision})
}

func (s *adminService) getTLSCertificates(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	certs, err := scanTLSCertificates(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not scan TLS certificates: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, certs)
}

func (s *adminService) getNodeDrainSimulation(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	nodeName := r.PathValue("name")
	sim, err := simulateNodeDrain(r.Context(), provisioner.client, nodeName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not run node drain simulation: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sim)
}

func (s *adminService) cordonNode(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	nodeName := r.PathValue("name")
	if err := toggleNodeCordon(r.Context(), provisioner.client, nodeName, true); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not cordon node: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "cordon", "Node", "", nodeName, "Node cordoned (marked unschedulable)")
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "cordoned", "node": nodeName})
}

func (s *adminService) uncordonNode(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	nodeName := r.PathValue("name")
	if err := toggleNodeCordon(r.Context(), provisioner.client, nodeName, false); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not uncordon node: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "uncordon", "Node", "", nodeName, "Node uncordoned (marked schedulable)")
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "uncordoned", "node": nodeName})
}

func (s *adminService) getWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	webhooks, err := fetchIncidentWebhooks(s.auth.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch webhooks: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, webhooks)
}

func (s *adminService) createWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Provider string `json:"provider"`
		Events   string `json:"events"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid webhook payload")
		return
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.URL) == "" {
		writeError(w, http.StatusBadRequest, "Name and Webhook URL are required")
		return
	}
	if input.Provider == "" {
		input.Provider = "generic"
	}
	id, err := insertIncidentWebhook(s.auth.db, input.Name, input.URL, input.Provider, input.Events)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create webhook: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "create", "Webhook", "", input.Name, fmt.Sprintf("Created incident webhook target (%s)", input.Provider))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": input.Name, "url": input.URL, "provider": input.Provider, "events": input.Events, "enabled": true})
}

func (s *adminService) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid webhook ID")
		return
	}
	wh, _ := getIncidentWebhook(s.auth.db, id)
	if err := deleteIncidentWebhook(s.auth.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete webhook: "+err.Error())
		return
	}
	name := idStr
	if wh != nil {
		name = wh.Name
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "delete", "Webhook", "", name, "Deleted incident webhook target")
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
}

func (s *adminService) testWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid webhook ID")
		return
	}
	wh, err := getIncidentWebhook(s.auth.db, id)
	if err != nil || wh == nil {
		writeError(w, http.StatusNotFound, "Webhook not found")
		return
	}
	testPayload := map[string]any{
		"title":        "[TEST ALERT] Crelan Incident Webhook Dispatcher",
		"severity":     "WARNING",
		"workload":     "production/orders-api",
		"summary":      "This is a verified test alert dispatched from Crelan Day-2 Operational Intelligence to validate the webhook integration.",
		"war_room_url": "/admin#war-room",
	}
	res, err := dispatchIncidentWebhook(r.Context(), *wh, testPayload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Webhook delivery failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *adminService) getDanglingConfigs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := scanDanglingConfigReferences(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not scan dangling configs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) injectDebugContainer(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	var req ephemeralDebugRequest
	_ = decodeJSON(w, r, &req)

	resp, err := injectEphemeralDebugContainer(r.Context(), provisioner.client, ns, name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not inject debug container: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "debug", "Pod", ns, name, fmt.Sprintf("Injected %s debug container (%s)", resp.ContainerName, resp.Mode))
	writeJSON(w, http.StatusOK, resp)
}

func (s *adminService) getEventStorm(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := calculateEventStormClusters(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not calculate event storm clusters: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) getAutoscaling(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := fetchAutoscalingAnalysis(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not fetch autoscaling report: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) getGitOpsDrift(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := scanGitOpsDrift(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not scan GitOps drift: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *adminService) reconcileGitOpsDrift(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	var input struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid reconcile payload")
		return
	}
	if err := reconcileGitOpsDrift(r.Context(), provisioner.client, input.Namespace, input.Kind, input.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "Reconciliation failed: "+err.Error())
		return
	}
	_ = recordPlatformAudit(s.auth.db, s.currentOperatorEmail(r), "reconcile", input.Kind, input.Namespace, input.Name, "Reconciled out-of-band drift back to declared GitOps last-applied configuration")
	writeJSON(w, http.StatusOK, map[string]any{"status": "reconciled", "kind": input.Kind, "name": input.Name})
}

func (s *adminService) getDisasterRecoveryRadar(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	provisioner := s.currentKubernetesProvisioner()
	if provisioner == nil || provisioner.client == nil {
		writeError(w, http.StatusServiceUnavailable, "No Kubernetes cluster is connected")
		return
	}
	report, err := scanDisasterRecoveryStatus(r.Context(), provisioner.client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not scan disaster recovery status: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}



