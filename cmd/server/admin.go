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
	"strings"
	"time"

	assets "crelan"
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
	var linked int
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM oauth_identities WHERE user_id = ? AND provider = 'github')`, userID).Scan(&linked); err != nil {
		return false
	}
	return linked == 1
}

func (s *adminService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /assets/admin.html", s.servePage)
	mux.HandleFunc("GET /api/v1/admin/overview", s.overview)
	mux.HandleFunc("GET /api/v1/admin/readiness", s.readiness)
	mux.HandleFunc("POST /api/v1/admin/clusters/connect", s.connectCluster)
	mux.HandleFunc("POST /api/v1/admin/control-plane-backup", s.downloadControlPlaneBackup)
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
