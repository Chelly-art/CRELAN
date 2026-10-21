package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
		{"pipeops-cluster-01", "PipeOps Cloud"},
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

func TestLensProEndpointsAuth(t *testing.T) {
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
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s got status %d, want %d", ep.method, ep.path, rec.Code, http.StatusUnauthorized)
		}
	}
}
