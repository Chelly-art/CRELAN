package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func createTestSession(t *testing.T, auth *authService, mux *http.ServeMux) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewBufferString(
		`{"name":"Token Tester","email":"tokens@example.com","password":"correct horse battery staple"}`))
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	t.Fatal("signup did not issue a session cookie")
	return nil
}

func TestWorkspaceTokenLifecycle(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)

	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewBufferString(
		`{"name":"CI deploy","scopes":["deployments:write","deployments:read"],"expires_in_days":30}`))
	createRequest.Host = "crelan.test"
	createRequest.Header.Set("Origin", "http://crelan.test")
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.AddCookie(cookie)
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create token status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created token: %v", err)
	}
	if created.ID == 0 || len(created.Token) < 40 || created.Token[:4] != "crl_" {
		t.Fatalf("unexpected created token: %+v", created)
	}
	var storedHash string
	if err := auth.db.QueryRow(`SELECT token_hash FROM api_tokens WHERE id = ?`, created.ID).Scan(&storedHash); err != nil {
		t.Fatalf("read stored token hash: %v", err)
	}
	if storedHash == created.Token || storedHash != hashToken(created.Token) {
		t.Fatal("raw token was stored instead of its hash")
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	listRequest.AddCookie(cookie)
	listResponse := httptest.NewRecorder()
	mux.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list tokens status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	if bytes.Contains(listResponse.Body.Bytes(), []byte(created.Token)) {
		t.Fatal("token list exposed the one-time token secret")
	}

	revokeRequest := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/tokens/%d", created.ID), nil)
	revokeRequest.Host = "crelan.test"
	revokeRequest.Header.Set("Origin", "http://crelan.test")
	revokeRequest.AddCookie(cookie)
	revokeResponse := httptest.NewRecorder()
	mux.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke token status = %d, body = %s", revokeResponse.Code, revokeResponse.Body.String())
	}
	var revokedAt string
	if err := auth.db.QueryRow(`SELECT revoked_at FROM api_tokens WHERE id = ?`, created.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("read revoked token: %v", err)
	}
	if revokedAt == "" {
		t.Fatal("token was not marked revoked")
	}
}

func TestWorkspaceTokenRejectsInvalidScopes(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewBufferString(
		`{"name":"Bad token","scopes":["admin:*"]}`))
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid token scope status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
