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
	"os"
	"path/filepath"
	"strings"
	"time"
)

// adminService provides platform-only endpoints. Platform access is configured
// explicitly through CRELAN_PLATFORM_ADMIN_EMAILS; workspace membership and
// workspace API tokens never grant platform access.
type adminService struct {
	auth        *authService
	provisioner *kubernetesProvisioner
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
	mux.HandleFunc("GET /api/v1/admin/overview", s.overview)
	mux.HandleFunc("GET /api/v1/admin/readiness", s.readiness)
	mux.HandleFunc("POST /api/v1/admin/control-plane-backup", s.downloadControlPlaneBackup)
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
	checks := []readinessCheck{}
	add := func(name string, ready bool, detail string) {
		checks = append(checks, readinessCheck{Name: name, Ready: ready, Detail: detail})
	}
	if err := s.auth.db.PingContext(r.Context()); err != nil {
		add("Control plane database", false, "SQLite connection failed")
	} else {
		add("Control plane database", true, "Connected; persistence mode is set by DATABASE_PATH")
	}
	if s.provisioner == nil {
		add("Kubernetes API", false, "No cluster credentials configured")
	} else {
		if _, err := s.provisioner.client.Discovery().ServerVersion(); err != nil {
			add("Kubernetes API", false, "Cluster API is unreachable")
		} else {
			add("Kubernetes API", true, "Cluster API is reachable")
		}
		checks = append(checks,
			apiGroupCheck(s.provisioner, "Capsule Tenant API", "capsule.clastix.io/v1beta2"),
			apiGroupCheck(s.provisioner, "CloudNativePG API", "postgresql.cnpg.io/v1"),
			apiGroupCheck(s.provisioner, "MySQL Operator API", "mysql.oracle.com/v2"),
		)
		add("Capsule tenant owner", s.provisioner.config.capsuleOwner != "", map[bool]string{true: "Configured", false: "Set CRELAN_CAPSULE_TENANT_OWNER"}[s.provisioner.config.capsuleOwner != ""])
		nodes, err := s.provisioner.client.CoreV1().Nodes().List(r.Context(), metav1.ListOptions{})
		if err != nil {
			add("Ready nodes", false, "Could not list cluster nodes")
		} else {
			ready := countReadyNodes(nodes.Items)
			zones := map[string]struct{}{}
			for _, node := range nodes.Items {
				if node.Labels[corev1.LabelTopologyZone] != "" {
					zones[node.Labels[corev1.LabelTopologyZone]] = struct{}{}
				}
			}
			add("Ready nodes", ready >= 3, fmt.Sprintf("%d ready; three are recommended for three-instance databases", ready))
			add("Failure zones", len(zones) >= 3, fmt.Sprintf("%d distinct zones; three are recommended for zone-level HA", len(zones)))
		}
		classes, err := s.provisioner.client.StorageV1().StorageClasses().List(r.Context(), metav1.ListOptions{})
		defaultClass := false
		if err == nil {
			for _, class := range classes.Items {
				if class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" || class.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
					defaultClass = true
					break
				}
			}
		}
		add("Default StorageClass", err == nil && defaultClass, map[bool]string{true: "A default StorageClass is configured", false: "Configure a durable default StorageClass"}[err == nil && defaultClass])
	}
	ready := true
	for _, check := range checks {
		if !check.Ready {
			ready = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": ready, "checks": checks})
}

func apiGroupCheck(provisioner *kubernetesProvisioner, name, groupVersion string) readinessCheck {
	_, err := provisioner.client.Discovery().ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		return readinessCheck{Name: name, Ready: false, Detail: "API resource unavailable or Crelan cannot discover it"}
	}
	return readinessCheck{Name: name, Ready: true, Detail: "API resource is discoverable; controller health still needs verification"}
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
	if s.provisioner == nil {
		return platformCluster{Error: "No Kubernetes cluster is configured"}
	}
	ctx := r.Context()
	nodes, err := s.provisioner.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return platformCluster{Error: "Could not list cluster nodes; check platform admin RBAC"}
	}
	spaces, err := s.provisioner.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: capsuleTenantLabel + ""})
	if err != nil {
		return platformCluster{Nodes: len(nodes.Items), ReadyNodes: countReadyNodes(nodes.Items), Error: "Could not list Capsule namespaces; check platform admin RBAC"}
	}
	namespaces := make([]string, 0, len(spaces.Items))
	for _, namespace := range spaces.Items {
		if namespace.Labels[capsuleTenantLabel] != "" {
			namespaces = append(namespaces, namespace.Name)
		}
	}
	return platformCluster{Connected: true, Nodes: len(nodes.Items), ReadyNodes: countReadyNodes(nodes.Items), TenantSpaces: len(namespaces), Namespaces: namespaces}
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
