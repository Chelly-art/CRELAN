package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestImageVulnerabilityScanning(t *testing.T) {
	// Test known CVE scanner matching on redis image
	res := scanImageForCVEs("redis:7.0.4-alpine", []string{"default/redis-leader"})
	if res.TotalCVEs == 0 {
		t.Fatalf("expected CVEs for vulnerable redis image, got 0")
	}
	if res.Critical == 0 && res.High == 0 {
		t.Fatalf("expected Critical or High severity CVEs, got crit=%d, high=%d", res.Critical, res.High)
	}
	if res.BaseOS != "Alpine Linux" {
		t.Fatalf("expected BaseOS Alpine Linux, got %s", res.BaseOS)
	}

	// Test clean or unknown image
	cleanRes := scanImageForCVEs("scratch-app:latest", []string{"default/worker"})
	if cleanRes.TotalCVEs != 0 {
		t.Fatalf("expected 0 CVEs for scratch-app, got %d", cleanRes.TotalCVEs)
	}
}

func TestHelmReleaseDecoder(t *testing.T) {
	rel := helmRawReleaseData{
		Name:      "vault",
		Namespace: "security",
		Version:   2,
	}
	rel.Info.Status = "deployed"
	rel.Chart.Metadata.Name = "vault"
	rel.Chart.Metadata.Version = "0.27.0"
	rel.Chart.Metadata.AppVersion = "1.15.2"
	rel.Config = map[string]any{"server": map[string]any{"ha": true}}

	rawJSON, err := json.Marshal(rel)
	if err != nil {
		t.Fatalf("marshal rel: %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(rawJSON); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	decoded, err := decodeHelmReleaseData(buf.Bytes())
	if err != nil {
		t.Fatalf("decode helm release: %v", err)
	}
	if decoded.Name != "vault" || decoded.Version != 2 {
		t.Fatalf("unexpected decoded: %+v", decoded)
	}
	if decoded.Chart.Metadata.Version != "0.27.0" {
		t.Fatalf("unexpected chart version: %s", decoded.Chart.Metadata.Version)
	}
}

func TestClusterFleetDetection(t *testing.T) {
	tests := []struct {
		context  string
		expected string
	}{
		{"arn:aws:eks:us-east-1:123:cluster/prod", "AWS EKS"},
		{"gke_project_us-central1_prod", "Google GKE"},
		{"aks-cluster-admin", "Azure AKS"},
		{"k3s-default", "K3s / Lightweight"},
		{"minikube", "Minikube Local"},
		{"cloud-cluster-01", "Managed Cloud"},
		{"custom-k8s", "Standard Kubernetes"},
	}

	for _, tc := range tests {
		provider := detectClusterProvider(tc.context)
		if provider != tc.expected {
			t.Errorf("detectClusterProvider(%q) = %q; want %q", tc.context, provider, tc.expected)
		}
	}
}

func TestEvaluateLocalRBAC(t *testing.T) {
	allowed, reason := evaluateLocalRBAC(t.Context(), nil, canIRequest{
		Namespace: "default",
		Verb:      "get",
		Resource:  "pods",
	})
	if allowed {
		t.Fatalf("expected false with nil client, got true")
	}
	if reason == "" {
		t.Fatalf("expected non-empty reason")
	}
}

func TestEnterpriseEndpointsAuth(t *testing.T) {
	auth := &authService{demoLoginEnabled: false}
	admin := newAdminService(auth)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/admin/security/image-vulnerabilities"},
		{"GET", "/api/v1/admin/helm/releases"},
		{"GET", "/api/v1/admin/crds"},
		{"POST", "/api/v1/admin/security/can-i"},
		{"GET", "/api/v1/admin/clusters/fleet"},
		{"POST", "/api/v1/admin/clusters/switch-context"},
		{"GET", "/api/v1/admin/clusters/customizations"},
		{"POST", "/api/v1/admin/clusters/customizations"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		switch ep.path {
		case "/api/v1/admin/security/image-vulnerabilities":
			admin.getImageVulnerabilities(rec, req)
		case "/api/v1/admin/helm/releases":
			admin.getHelmReleases(rec, req)
		case "/api/v1/admin/crds":
			admin.getCRDs(rec, req)
		case "/api/v1/admin/security/can-i":
			admin.checkCanI(rec, req)
		case "/api/v1/admin/clusters/fleet":
			admin.getClusterFleet(rec, req)
		case "/api/v1/admin/clusters/switch-context":
			admin.switchClusterContext(rec, req)
		case "/api/v1/admin/clusters/customizations":
			if ep.method == "GET" {
				admin.getClusterCustomizations(rec, req)
			} else {
				admin.saveClusterCustomization(rec, req)
			}
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s got status %d, want %d", ep.method, ep.path, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestClusterFleetAndCustomizations(t *testing.T) {
	t.Setenv("CRELAN_DEMO_LOGIN", "true")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("newAuthService: %v", err)
	}
	defer auth.db.Close()
	auth.demoLoginEnabled = true

	// Seed admin user and session
	sessionToken := "admin-test-token"
	now := time.Now().UTC()
	if _, err := auth.db.Exec(`INSERT INTO users (id, name, email, created_at) VALUES (1, 'Admin', 'admin@crelan.local', ?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	if _, err := auth.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, 1, ?, ?)`,
		hashToken(sessionToken), now.Add(time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	admin := newAdminService(auth)

	targetContext := "in-cluster"
	if contexts, _, err := loadKubeconfigContexts(); err == nil && len(contexts) > 0 {
		targetContext = contexts[0]
	}

	// 1. Save customization
	customPayload := fmt.Sprintf(`{"context_name": %q, "color": "#ef4444", "alias": "Production US-East", "environment": "production"}`, targetContext)
	req := httptest.NewRequest("POST", "/api/v1/admin/clusters/customizations", bytes.NewBufferString(customPayload))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	rec := httptest.NewRecorder()
	admin.saveClusterCustomization(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("save customization status %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Fetch customizations
	reqGet := httptest.NewRequest("GET", "/api/v1/admin/clusters/customizations", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	recGet := httptest.NewRecorder()
	admin.getClusterCustomizations(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("get customizations status %d: %s", recGet.Code, recGet.Body.String())
	}

	var customMap map[string]clusterCustomization
	if err := json.Unmarshal(recGet.Body.Bytes(), &customMap); err != nil {
		t.Fatalf("unmarshal customizations: %v", err)
	}
	if customMap[targetContext].Color != "#ef4444" {
		t.Errorf("expected color #ef4444, got %s", customMap[targetContext].Color)
	}
	if customMap[targetContext].Alias != "Production US-East" {
		t.Errorf("expected alias 'Production US-East', got %s", customMap[targetContext].Alias)
	}

	// 3. Fetch cluster fleet and verify customization merged
	reqFleet := httptest.NewRequest("GET", "/api/v1/admin/clusters/fleet", nil)
	reqFleet.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	recFleet := httptest.NewRecorder()
	admin.getClusterFleet(recFleet, reqFleet)

	if recFleet.Code != http.StatusOK {
		t.Fatalf("get fleet status %d: %s", recFleet.Code, recFleet.Body.String())
	}

	var fleet []clusterFleetMember
	if err := json.Unmarshal(recFleet.Body.Bytes(), &fleet); err != nil {
		t.Fatalf("unmarshal fleet: %v", err)
	}
	if len(fleet) == 0 {
		t.Fatalf("expected at least 1 fleet member, got 0")
	}

	found := false
	for _, m := range fleet {
		if m.Name == targetContext {
			found = true
			if m.Color != "#ef4444" {
				t.Errorf("expected fleet member color #ef4444, got %s", m.Color)
			}
			if m.Alias != "Production US-East" {
				t.Errorf("expected fleet member alias 'Production US-East', got %s", m.Alias)
			}
		}
	}
	if !found {
		t.Errorf("expected %s in fleet, but not found", targetContext)
	}
}

func TestMultiClusterPersistenceAndFleet(t *testing.T) {
	t.Setenv("CRELAN_DEMO_LOGIN", "true")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("newAuthService: %v", err)
	}
	defer auth.db.Close()
	auth.demoLoginEnabled = true

	sessionToken := "admin-multi-token"
	now := time.Now().UTC()
	if _, err := auth.db.Exec(`INSERT INTO users (id, name, email, created_at) VALUES (1, 'Admin', 'admin@crelan.local', ?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	if _, err := auth.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, 1, ?, ?)`,
		hashToken(sessionToken), now.Add(time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	deployments := &deploymentService{
		db:        auth.db,
		auth:      auth,
		providers: make(map[string]DeploymentProvisioner),
	}
	admin := newAdminService(auth)
	admin.deployments = deployments

	dummyKubeconfig1 := `apiVersion: v1
clusters:
- cluster:
    server: https://10.0.0.1:6443
  name: cluster-alpha
contexts:
- context:
    cluster: cluster-alpha
    user: user-alpha
  name: cluster-alpha
current-context: cluster-alpha
kind: Config
users:
- name: user-alpha
  user:
    token: token-alpha`

	dummyKubeconfig2 := `apiVersion: v1
clusters:
- cluster:
    server: https://10.0.0.2:6443
  name: cluster-beta
contexts:
- context:
    cluster: cluster-beta
    user: user-beta
  name: cluster-beta
current-context: cluster-beta
kind: Config
users:
- name: user-beta
  user:
    token: token-beta`

	// 1. Connect Cluster 1
	if err := saveKubeconfigCredential(auth.db, "cluster-alpha", dummyKubeconfig1); err != nil {
		t.Fatalf("save cluster-alpha: %v", err)
	}

	// 2. Connect Cluster 2 - must NOT overwrite Cluster 1
	if err := saveKubeconfigCredential(auth.db, "cluster-beta", dummyKubeconfig2); err != nil {
		t.Fatalf("save cluster-beta: %v", err)
	}

	// 3. Verify database contains BOTH clusters
	var count int
	if err := auth.db.QueryRow(`SELECT COUNT(*) FROM platform_cluster_config`).Scan(&count); err != nil {
		t.Fatalf("count clusters: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 clusters in database, got %d", count)
	}

	// 4. Verify allKnownContexts includes both
	contexts := deployments.allKnownContexts()
	hasAlpha, hasBeta := false, false
	for _, c := range contexts {
		if c == "cluster-alpha" {
			hasAlpha = true
		}
		if c == "cluster-beta" {
			hasBeta = true
		}
	}
	if !hasAlpha || !hasBeta {
		t.Fatalf("expected allKnownContexts to have cluster-alpha and cluster-beta, got: %v", contexts)
	}

	// 5. Test getClusterFleet returns BOTH clusters
	reqFleet := httptest.NewRequest("GET", "/api/v1/admin/clusters/fleet", nil)
	reqFleet.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	recFleet := httptest.NewRecorder()
	admin.getClusterFleet(recFleet, reqFleet)

	if recFleet.Code != http.StatusOK {
		t.Fatalf("get fleet status %d: %s", recFleet.Code, recFleet.Body.String())
	}

	var fleet []clusterFleetMember
	if err := json.Unmarshal(recFleet.Body.Bytes(), &fleet); err != nil {
		t.Fatalf("unmarshal fleet: %v", err)
	}

	fleetAlpha, fleetBeta := false, false
	for _, m := range fleet {
		if m.Name == "cluster-alpha" {
			fleetAlpha = true
		}
		if m.Name == "cluster-beta" {
			fleetBeta = true
		}
	}
	if !fleetAlpha || !fleetBeta {
		t.Fatalf("expected fleet to contain both cluster-alpha and cluster-beta, got fleet: %+v", fleet)
	}

	// 6. Test disconnect cluster-alpha
	discPayload := `{"context": "cluster-alpha"}`
	reqDisc := httptest.NewRequest("POST", "/api/v1/admin/clusters/disconnect", bytes.NewBufferString(discPayload))
	reqDisc.Header.Set("Content-Type", "application/json")
	reqDisc.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	recDisc := httptest.NewRecorder()
	admin.disconnectCluster(recDisc, reqDisc)

	if recDisc.Code != http.StatusOK {
		t.Fatalf("disconnect status %d: %s", recDisc.Code, recDisc.Body.String())
	}

	// 7. Verify cluster-alpha is deleted and cluster-beta remains
	var remainingCount int
	if err := auth.db.QueryRow(`SELECT COUNT(*) FROM platform_cluster_config`).Scan(&remainingCount); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remainingCount != 1 {
		t.Fatalf("expected 1 cluster remaining after disconnect, got %d", remainingCount)
	}
}

