package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSignupCreatesWorkspaceAndSession(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()

	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	request := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewBufferString(
		`{"name":"Ada Lovelace","email":"ada@example.com","password":"correct horse battery staple"}`))
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, body = %s", response.Code, response.Body.String())
	}
	var user authUser
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatalf("decode signup response: %v", err)
	}
	if user.Email != "ada@example.com" || user.WorkspaceID == 0 || user.Workspace != "Ada Lovelace's workspace" {
		t.Fatalf("unexpected signup identity: %+v", user)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("expected an HttpOnly session cookie, got %v", cookies)
	}

	currentRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	currentRequest.AddCookie(cookies[0])
	currentResponse := httptest.NewRecorder()
	mux.ServeHTTP(currentResponse, currentRequest)
	if currentResponse.Code != http.StatusOK {
		t.Fatalf("current user status = %d, body = %s", currentResponse.Code, currentResponse.Body.String())
	}
	dashboardRequest := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	dashboardRequest.AddCookie(cookies[0])
	dashboardResponse := httptest.NewRecorder()
	mux.ServeHTTP(dashboardResponse, dashboardRequest)
	if dashboardResponse.Code != http.StatusOK ||
		!bytes.Contains(dashboardResponse.Body.Bytes(), []byte("CLUSTER BROWSER")) {
		t.Fatalf("authenticated dashboard status = %d, body = %s", dashboardResponse.Code, dashboardResponse.Body.String())
	}
}

func TestSignupRejectsCrossOriginRequest(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}

	defer auth.db.Close()

	request := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewBufferString(
		`{"name":"Ada","email":"ada@example.com","password":"correct horse battery staple"}`))
	request.Host = "crelan.test"
	request.Header.Set("Origin", "https://attacker.test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	auth.signup(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin signup status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestLandingPageIsPublic(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()

	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("landing page status = %d, want %d", response.Code, http.StatusOK)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`href="/login"`)) {
		t.Fatal("landing page must provide a workspace link")
	}
}

func TestDashboardRequiresSignIn(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	auth.dashboardPage(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("dashboard status = %d, want redirect to sign-in", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/login" {
		t.Fatalf("dashboard redirect = %q, want /login", location)
	}
}

func TestLoginAcceptsAnyCredentials(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	t.Setenv("CRELAN_DEMO_LOGIN", "false")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()

	request := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(
		`{"identity":"any-user-name","password":"not-a-real-password"}`))
	request.RemoteAddr = "192.0.2.10:54321"
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	auth.login(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("open demo login status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestDemoLoginCreatesLocalAdminWorkspace(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	t.Setenv("CRELAN_DEMO_LOGIN", "true")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()

	request := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(
		`{"identity":"admin","password":"pass"}`))
	request.RemoteAddr = "127.0.0.1:54321"
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	auth.login(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("demo login status = %d, body = %s", response.Code, response.Body.String())
	}
	var user authUser
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatalf("decode demo login response: %v", err)
	}
	if user.Name != "Admin" || user.Workspace != "Admin workspace" {
		t.Fatalf("unexpected demo identity: %+v", user)
	}
}

func TestDemoLoginAcceptsNonLoopbackRequest(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	t.Setenv("CRELAN_DEMO_LOGIN", "true")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()

	request := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(
		`{"identity":"admin","password":"pass"}`))
	request.RemoteAddr = "192.0.2.10:54321"
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	auth.login(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("demo login status = %d, body = %s", response.Code, response.Body.String())
	}
}
