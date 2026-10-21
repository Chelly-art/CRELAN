package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestGitHubAppConfigurationAndJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate app key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	t.Setenv("CRELAN_GITHUB_APP_ID", "12345")
	t.Setenv("CRELAN_GITHUB_APP_SLUG", "crelan-deployer")
	t.Setenv("CRELAN_GITHUB_APP_PRIVATE_KEY", string(pemKey))
	t.Setenv("CRELAN_GITHUB_APP_WEBHOOK_SECRET", "webhook-secret")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	service, err := newGitHubAppService(auth.db, auth)
	if err != nil {
		t.Fatalf("initialize GitHub App: %v", err)
	}
	token, err := service.appJWT()
	if err != nil {
		t.Fatalf("create app JWT: %v", err)
	}
	parts := bytes.Split([]byte(token), []byte("."))
	if len(parts) != 3 {
		t.Fatalf("JWT has %d components", len(parts))
	}
	unsigned := string(parts[0]) + "." + string(parts[1])
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := base64.RawURLEncoding.DecodeString(string(parts[2]))
	if err != nil {
		t.Fatalf("decode JWT signature: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("verify generated JWT: %v", err)
	}
	var claims struct {
		Issuer string `json:"iss"`
		Issued int64  `json:"iat"`
		Expiry int64  `json:"exp"`
	}
	claimBytes, err := base64.RawURLEncoding.DecodeString(string(parts[1]))
	if err != nil {
		t.Fatalf("decode JWT claims: %v", err)
	}
	if err := json.Unmarshal(claimBytes, &claims); err != nil {
		t.Fatalf("parse JWT claims: %v", err)
	}
	if claims.Issuer != "12345" || claims.Expiry-claims.Issued != 9*60 {
		t.Fatalf("unexpected GitHub App JWT claims: %+v", claims)
	}
}

func TestGitHubAppWebhookSignature(t *testing.T) {
	service := &githubAppService{webhookSecret: []byte("hook-secret")}
	body := []byte(`{"action":"push"}`)
	mac := hmac.New(sha256.New, service.webhookSecret)
	_, _ = mac.Write(body)
	if !service.validWebhookSignature(body, mac.Sum(nil)) {
		t.Fatal("valid webhook signature was rejected")
	}
	if service.validWebhookSignature([]byte(`{"action":"edited"}`), mac.Sum(nil)) {
		t.Fatal("invalid webhook signature was accepted")
	}
}

func TestGitHubRepositoryKeyNormalizesCloneURL(t *testing.T) {
	if got := githubRepositoryKey("https://github.com/Acme/Storefront.git"); got != "acme/storefront" {
		t.Fatalf("repository key = %q", got)
	}
}

func TestGitHubPushQueuesDefaultBranchDeployment(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)
	identityRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	identityRequest.AddCookie(cookie)
	_, workspaceID, err := auth.sessionWorkspace(identityRequest)
	if err != nil {
		t.Fatalf("read test workspace: %v", err)
	}
	_, err = auth.db.Exec(`INSERT INTO github_app_installations (workspace_id, installation_id, account_login, created_at) VALUES (?, ?, ?, ?)`,
		workspaceID, 987, "acme", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("create installation mapping: %v", err)
	}
	deployments := &deploymentService{db: auth.db}
	_, _, _, err = deployments.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "storefront", RepositoryURL: "https://github.com/acme/storefront", Dockerfile: "Dockerfile", Port: 8080,
	})
	if err != nil {
		t.Fatalf("queue initial deployment: %v", err)
	}
	app := &githubAppService{db: auth.db, deployments: deployments}
	payload, err := json.Marshal(map[string]any{
		"ref":          "refs/heads/main",
		"installation": map[string]any{"id": 987},
		"repository": map[string]any{
			"clone_url":      "https://github.com/acme/storefront.git",
			"default_branch": "main",
		},
	})
	if err != nil {
		t.Fatalf("encode push event: %v", err)
	}
	if err := app.handlePushEvent(context.Background(), payload); err != nil {
		t.Fatalf("handle push event: %v", err)
	}
	var count int
	var branch string
	if err := auth.db.QueryRow(`SELECT COUNT(*) FROM deployment_runs`).Scan(&count); err != nil {
		t.Fatalf("count deployment runs: %v", err)
	}
	if err := auth.db.QueryRow(`SELECT branch FROM deployments WHERE workspace_id = ? AND name = ?`,
		workspaceID, "storefront").Scan(&branch); err != nil {
		t.Fatalf("read triggered deployment: %v", err)
	}
	if count != 2 || branch != "main" {
		t.Fatalf("push generated %d runs and branch %q, want 2 runs and main", count, branch)
	}
}

func TestGitHubInstallationTokenRequiresNoStoredToken(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	service := &githubAppService{db: auth.db, configured: true}
	token, err := service.installationToken(context.Background(), 999)
	if err != nil || token != "" {
		t.Fatalf("installation token = %q, error = %v, want no token without an installation", token, err)
	}
}

func TestGitHubWebhookEndpointValidatesSignature(t *testing.T) {
	service := &githubAppService{configured: true, webhookSecret: []byte("secret")}
	mux := http.NewServeMux()
	service.registerRoutes(mux)
	body := []byte(`{}`)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", "sha256=00")
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("webhook status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
