package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractAPIURL(t *testing.T) {
	args, apiURL, err := extractAPIURL(
		[]string{"--name", "shop", "--api-url", "http://localhost:8080", "--repo", "https://github.com/acme/shop"},
		"https://api.crelan.example",
	)
	if err != nil {
		t.Fatalf("extract API URL: %v", err)
	}
	if apiURL != "http://localhost:8080" || len(args) != 4 {
		t.Fatalf("unexpected parsed args=%v apiURL=%q", args, apiURL)
	}
}

func TestParseBaseURLRejectsInsecureRemoteHTTP(t *testing.T) {
	if _, err := parseBaseURL("http://api.crelan.example"); err == nil {
		t.Fatal("expected remote HTTP URL to be rejected")
	}
}

func TestRunRequiresToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{
		"deploy",
		"--api-url", "http://localhost:8080",
		"--name", "storefront",
		"--repo", "https://github.com/acme/storefront",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "CRELAN_TOKEN") {
		t.Fatalf("run error = %v, want missing token message", err)
	}
}

func TestDeployReportsUnavailableProvider(t *testing.T) {
	t.Setenv("CRELAN_TOKEN", "crl_example")
	var payload struct {
		Port int `json:"port"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/deployments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode deployment payload: %v", err)
		}
		if r.Header.Get("Authorization") != "Bearer crl_example" {
			t.Error("workspace token was not sent as bearer auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"Deployment provider is not configured","code":"provisioner_unavailable"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"deploy", "--api-url", server.URL,
		"--name", "storefront", "--repo", "https://github.com/acme/storefront",
		"--port", "3000",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "provisioner_unavailable") {
		t.Fatalf("deploy error = %v, want provider unavailable response", err)
	}
	if payload.Port != 3000 {
		t.Fatalf("deployment port = %d, want 3000", payload.Port)
	}
}

func TestDeployWaitsForRunningStatus(t *testing.T) {
	t.Setenv("CRELAN_TOKEN", "crl_example")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/deployments":
			_, _ = w.Write([]byte(`{"id":7,"status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/deployments/7":
			_, _ = w.Write([]byte(`{"id":7,"status":"running","url":"https://app.example.test"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"deploy", "--api-url", server.URL,
		"--name", "storefront", "--repo", "https://github.com/acme/storefront", "--wait",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("wait for deployment: %v", err)
	}
	if !strings.Contains(stdout.String(), `"status": "running"`) {
		t.Fatalf("CLI did not print final deployment status: %s", stdout.String())
	}
}

func TestRunDoesNotPrintWorkspaceToken(t *testing.T) {
	t.Setenv("CRELAN_TOKEN", "crl_sensitive_example")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"deployments", "--api-url", server.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if strings.Contains(stdout.String(), "crl_sensitive_example") || strings.Contains(stderr.String(), "crl_sensitive_example") {
		t.Fatal("CLI printed the workspace token")
	}
}
