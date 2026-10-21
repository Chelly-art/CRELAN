package main

import (
	"context"
	"database/sql"
	"errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	assets "crelan"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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
	mux.HandleFunc("GET /api/v1/admin/databases/topology", s.getDatabaseTopology)
	mux.HandleFunc("GET /api/v1/admin/resources/efficiency", s.getResourceEfficiency)
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
		Kubeconfig string `json:"kubeconfig"`
		Context    string `json:"context"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Paste a valid kubeconfig (maximum 1 MiB)")
		return
	}
	if strings.TrimSpace(input.Kubeconfig) == "" {
		writeError(w, http.StatusBadRequest, "Paste a kubeconfig to connect a cluster")
		return
	}
	config, err := clientcmd.Load([]byte(input.Kubeconfig))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not parse that kubeconfig")
		return
	}
	contextName := strings.TrimSpace(input.Context)
	if contextName == "" {
		contextName = config.CurrentContext
	}
	if contextName == "" && len(config.Contexts) == 1 {
		for name := range config.Contexts {
			contextName = name
		}
	}
	if contextName == "" || config.Contexts[contextName] == nil {
		writeError(w, http.StatusBadRequest, "Enter a context name that exists in the pasted kubeconfig")
		return
	}
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*config, contextName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		log.Printf("build Kubernetes client config for context %q: %v", contextName, err)
		writeError(w, http.StatusBadRequest, "Could not load credentials for that context")
		return
	}
	provisioner, err := newKubernetesProvisionerWithRESTConfig(restConfig)
	if err != nil {
		log.Printf("initialize Kubernetes client for context %q: %v", contextName, err)
		writeError(w, http.StatusBadRequest, "Could not initialize a client for that cluster")
		return
	}
	if _, err := provisioner.client.Discovery().ServerVersion(); err != nil {
		log.Printf("connect Kubernetes context %q: %v", contextName, err)
		writeError(w, http.StatusBadGateway, "Could not reach that cluster with the pasted credentials")
		return
	}
	if s.deployments == nil {
		writeError(w, http.StatusServiceUnavailable, "Deployment service is not available")
		return
	}
	if s.auth.databasePath == ":memory:" {
		writeError(w, http.StatusConflict, "Set DATABASE_PATH to a durable file before saving a cluster connection")
		return
	}
	if err := saveKubeconfigCredential(s.auth.db, contextName, input.Kubeconfig); err != nil {
		if errors.Is(err, errKubeconfigEncryptionKeyMissing) || strings.Contains(err.Error(), "CRELAN_KUBECONFIG_ENCRYPTION_KEY") {
			writeError(w, http.StatusServiceUnavailable, "Configure CRELAN_KUBECONFIG_ENCRYPTION_KEY as a base64-encoded 32-byte key before connecting a cluster")
			return
		}
		log.Printf("save encrypted kubeconfig for context %q: %v", contextName, err)
		writeError(w, http.StatusInternalServerError, "Could not securely save the cluster connection")
		return
	}
	s.deployments.setCluster(provisioner, contextName)
	writeJSON(w, http.StatusOK, map[string]string{"context": contextName, "status": "connected"})
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
		http.Redirect(w, r, "/login", http.StatusSeeOther)
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
	_, wsMap, _ := fetchWorkspaceMappings(r.Context(), s.auth.db)
	namespace := r.URL.Query().Get("namespace")
	pods, err := fetchPodsList(r.Context(), provisioner.client, namespace, wsMap)
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
	list, err := fetchNamespacesList(r.Context(), provisioner.client)
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
	case "ingress", "ingresses":
		rawObj, err = provisioner.client.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
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

