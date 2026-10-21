package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestAccessDB(t *testing.T) (*sql.DB, *authService) {
	t.Helper()
	t.Setenv("CRELAN_DEMO_LOGIN", "true")
	t.Setenv("CRELAN_PLATFORM_ADMIN_EMAILS", "alice@test.local")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("newAuthService: %v", err)
	}
	db := auth.db

	// Insert test user and workspace
	_, err = db.Exec(`
		INSERT INTO users (id, name, email, password_hash, created_at)
		VALUES (1, 'Alice Owner', 'alice@test.local', 'hash', '2026-01-01');
		INSERT INTO users (id, name, email, password_hash, created_at)
		VALUES (2, 'Bob Dev', 'bob@test.local', 'hash', '2026-01-01');
		INSERT INTO users (id, name, email, password_hash, created_at)
		VALUES (3, 'Charlie Viewer', 'charlie@test.local', 'hash', '2026-01-01');

		INSERT INTO workspaces (id, name, created_at)
		VALUES (1, 'Main Workspace', '2026-01-01');

		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES (1, 1, 'owner'), (1, 2, 'developer'), (1, 3, 'viewer');

		INSERT INTO oauth_identities (provider, provider_user_id, user_id)
		VALUES ('demo', 'demo-alice', 1);
	`)
	if err != nil {
		t.Fatalf("populate test db: %v", err)
	}

	_ = ensureWorkspaceSessionSchema(db)

	return db, auth
}

func TestCheckUserAccess(t *testing.T) {
	db, _ := setupTestAccessDB(t)
	defer db.Close()
	ctx := context.Background()

	// 1. Owner has unconditional admin access
	allowed, reason, err := CheckUserAccess(ctx, db, 1, 1, "cluster", "prod-cluster", "admin")
	if err != nil || !allowed {
		t.Fatalf("expected owner to have admin access, got %v (%s), err: %v", allowed, reason, err)
	}

	// 2. Default developer has read & write, but NOT admin
	allowed, _, _ = CheckUserAccess(ctx, db, 2, 1, "cluster", "staging-cluster", "read")
	if !allowed {
		t.Errorf("developer should have read access")
	}
	allowed, _, _ = CheckUserAccess(ctx, db, 2, 1, "cluster", "staging-cluster", "write")
	if !allowed {
		t.Errorf("developer should have write access")
	}
	allowed, _, _ = CheckUserAccess(ctx, db, 2, 1, "cluster", "staging-cluster", "admin")
	if allowed {
		t.Errorf("developer should NOT have admin access without explicit grant")
	}

	// 3. Default viewer has read, but NOT write or admin
	allowed, _, _ = CheckUserAccess(ctx, db, 3, 1, "deployment", "payment-service", "read")
	if !allowed {
		t.Errorf("viewer should have read access")
	}
	allowed, _, _ = CheckUserAccess(ctx, db, 3, 1, "deployment", "payment-service", "write")
	if allowed {
		t.Errorf("viewer should NOT have write access")
	}

	// 4. Add explicit grant for Charlie on "prod-cluster" with "write" permission
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO workspace_access_grants (workspace_id, user_id, resource_type, resource_id, permission, created_at, updated_at)
		VALUES (1, 3, 'cluster', 'prod-cluster', 'write', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert grant: %v", err)
	}

	// Charlie should now have write access on prod-cluster
	allowed, reason, _ = CheckUserAccess(ctx, db, 3, 1, "cluster", "prod-cluster", "write")
	if !allowed {
		t.Errorf("charlie should have write access on prod-cluster with explicit grant, got %s", reason)
	}
	// But still NOT admin
	allowed, _, _ = CheckUserAccess(ctx, db, 3, 1, "cluster", "prod-cluster", "admin")
	if allowed {
		t.Errorf("charlie should NOT have admin access")
	}
	// And on another cluster, Charlie remains only a viewer (read-only)
	allowed, _, _ = CheckUserAccess(ctx, db, 3, 1, "cluster", "other-cluster", "write")
	if allowed {
		t.Errorf("charlie should NOT have write access on other-cluster")
	}
}

func TestAccessGrantsHTTPHandlers(t *testing.T) {
	db, auth := setupTestAccessDB(t)
	defer db.Close()

	admin := &adminService{
		auth:        auth,
		adminEmails: map[string]struct{}{"alice@test.local": {}},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/access/grants", admin.listAccessGrants)
	mux.HandleFunc("POST /api/v1/admin/access/grants", admin.createOrUpdateAccessGrant)
	mux.HandleFunc("DELETE /api/v1/admin/access/grants/{id}", admin.deleteAccessGrant)
	mux.HandleFunc("POST /api/v1/admin/access/check", admin.checkAccessEndpoint)

	// Create a session for Alice (user 1, workspace 1)
	token := "test-session-alice"
	tokenHash := hashToken(token)
	expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano)
	_, err := db.Exec(`INSERT INTO sessions (user_id, token_hash, expires_at, created_at, workspace_id) VALUES (1, ?, ?, '2026-01-01', 1)`, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// 1. Create a grant via POST
	body, _ := json.Marshal(map[string]any{
		"user_id":       2,
		"resource_type": "cluster",
		"resource_id":   "production-eks",
		"permission":    "admin",
	})
	req := httptest.NewRequest("POST", "/api/v1/admin/access/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 on create grant, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. List grants via GET
	req = httptest.NewRequest("GET", "/api/v1/admin/access/grants", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 on list grants, got %d: %s", rec.Code, rec.Body.String())
	}

	var listResp struct {
		Grants  []workspaceAccessGrant `json:"grants"`
		Members []workspaceMemberView  `json:"members"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(listResp.Grants))
	}
	if listResp.Grants[0].ResourceID != "production-eks" || listResp.Grants[0].Permission != "admin" {
		t.Errorf("unexpected grant values: %+v", listResp.Grants[0])
	}

	// 3. Check access via POST /api/v1/admin/access/check
	checkBody, _ := json.Marshal(map[string]any{
		"user_id":             2,
		"resource_type":       "cluster",
		"resource_id":         "production-eks",
		"required_permission": "admin",
	})
	req = httptest.NewRequest("POST", "/api/v1/admin/access/check", bytes.NewReader(checkBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 on check access, got %d: %s", rec.Code, rec.Body.String())
	}
	var checkResp accessCheckResult
	_ = json.NewDecoder(rec.Body).Decode(&checkResp)
	if !checkResp.Allowed {
		t.Errorf("expected check to be allowed, got false (%s)", checkResp.Reason)
	}

	// 4. Delete the grant via DELETE
	grantID := listResp.Grants[0].ID
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/admin/access/grants/%d", grantID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", grantID))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 on delete grant, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentManifestHandler(t *testing.T) {
	t.Setenv("CRELAN_PLATFORM_ADMIN_EMAILS", "alice@test.local")
	db, auth := setupTestAccessDB(t)
	defer db.Close()

	admin := &adminService{
		auth:        auth,
		adminEmails: map[string]struct{}{"alice@test.local": {}},
	}

	token := "test-session-alice"
	tokenHash := hashToken(token)
	expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO sessions (user_id, token_hash, expires_at, created_at, workspace_id) VALUES (1, ?, ?, '2026-01-01', 1)`, tokenHash, expiresAt); err != nil {
		t.Fatalf("session insert: %v", err)
	}

	// JSON format request
	req := httptest.NewRequest("GET", "/api/v1/admin/clusters/agent-manifest", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	admin.getAgentManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var data map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&data); err != nil {
		t.Fatalf("decode agent manifest json: %v", err)
	}
	if data["cluster_token"] == "" || data["install_command"] == "" {
		t.Errorf("expected cluster_token and install_command in response")
	}

	// YAML format request
	reqYAML := httptest.NewRequest("GET", "/api/v1/admin/clusters/agent-manifest?format=yaml", nil)
	reqYAML.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	recYAML := httptest.NewRecorder()
	admin.getAgentManifest(recYAML, reqYAML)

	if recYAML.Code != http.StatusOK {
		t.Fatalf("expected status 200 for YAML, got %d", recYAML.Code)
	}
	if !bytes.Contains(recYAML.Body.Bytes(), []byte("kind: Deployment")) {
		t.Errorf("expected YAML to contain Deployment manifest")
	}
}

func TestConnectClusterWithServiceAccountToken(t *testing.T) {
	db, auth := setupTestAccessDB(t)
	defer db.Close()

	admin := &adminService{
		auth:        auth,
		adminEmails: map[string]struct{}{"alice@test.local": {}},
	}

	token := "test-session-alice"
	tokenHash := hashToken(token)
	expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano)
	_, _ = db.Exec(`INSERT INTO sessions (user_id, token_hash, expires_at, created_at, workspace_id) VALUES (1, ?, ?, '2026-01-01', 1)`, tokenHash, expiresAt)

	// Missing URL
	bodyMissingURL, _ := json.Marshal(map[string]any{
		"method":       "token",
		"bearer_token": "some-jwt-token",
	})
	req := httptest.NewRequest("POST", "/api/v1/admin/clusters/connect", bytes.NewReader(bodyMissingURL))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	admin.connectCluster(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing URL, got %d", rec.Code)
	}

	// Missing Token
	bodyMissingToken, _ := json.Marshal(map[string]any{
		"method":         "token",
		"api_server_url": "https://10.0.0.1:6443",
	})
	req = httptest.NewRequest("POST", "/api/v1/admin/clusters/connect", bytes.NewReader(bodyMissingToken))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec = httptest.NewRecorder()
	admin.connectCluster(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing token, got %d", rec.Code)
	}
}
