package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type testProvisioner struct {
	workspaceID int64
	request     DeploymentRequest
	deletedName string
}

type blockingDeploymentProvisioner struct {
	started chan struct{}
}

type customDomainTestProvisioner struct {
	host string
}

func (*customDomainTestProvisioner) Deploy(context.Context, int64, DeploymentRequest) (ProvisionedDeployment, error) {
	return ProvisionedDeployment{Status: "running"}, nil
}

func (*customDomainTestProvisioner) Delete(context.Context, int64, string) error { return nil }

func (p *customDomainTestProvisioner) ConfigureCustomDomain(_ context.Context, _ int64, _ string, host, _ string) error {
	p.host = host
	return nil
}

func (*customDomainTestProvisioner) DeleteCustomDomain(context.Context, int64, string, string) error {
	return nil
}

func (p *blockingDeploymentProvisioner) Deploy(ctx context.Context, _ int64, _ DeploymentRequest) (ProvisionedDeployment, error) {
	close(p.started)
	<-ctx.Done()
	return ProvisionedDeployment{}, ctx.Err()
}

func (*blockingDeploymentProvisioner) Delete(context.Context, int64, string) error {
	return nil
}

func (p *testProvisioner) Deploy(_ context.Context, workspaceID int64, request DeploymentRequest) (ProvisionedDeployment, error) {
	p.workspaceID = workspaceID
	p.request = request
	return ProvisionedDeployment{Status: "running"}, nil
}

func (p *testProvisioner) Delete(_ context.Context, workspaceID int64, name string) error {
	p.workspaceID = workspaceID
	p.deletedName = name
	return nil
}

func TestDeploymentRequiresConfiguredProvisioner(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	service := &deploymentService{db: auth.db, auth: auth}
	service.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", bytes.NewBufferString(
		`{"name":"storefront","repository_url":"https://github.com/acme/storefront"}`))
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("deployment status = %d, want %d, body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	var count int
	if err := auth.db.QueryRow(`SELECT COUNT(*) FROM deployments`).Scan(&count); err != nil {
		t.Fatalf("count deployment rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("unconfigured provider created %d deployment records", count)
	}
}

func TestDeploymentUsesWorkspaceProvisionerAndPersistsStatus(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	provisioner := &testProvisioner{}
	service := &deploymentService{db: auth.db, auth: auth, provisioner: provisioner}
	service.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", bytes.NewBufferString(
		`{"name":"storefront","repository_url":"https://github.com/acme/storefront","branch":"main","port":3000}`))
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deployment status = %d, body = %s", response.Code, response.Body.String())
	}
	var result deployment
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	if result.Status != "queued" || result.Dockerfile != "Dockerfile" || result.Port != 3000 ||
		result.LatestRunID == 0 || provisioner.workspaceID != 0 {
		t.Fatalf("unexpected provisioned deployment: result=%+v provider=%+v", result, provisioner)
	}
	if err := service.processNextRun(context.Background()); err != nil {
		t.Fatalf("process queued deployment: %v", err)
	}
	if provisioner.workspaceID == 0 || provisioner.request.Name != "storefront" || provisioner.request.Port != 3000 {
		t.Fatalf("deployment worker received unexpected request: %+v", provisioner)
	}
	var savedStatus string
	var savedPort int
	if err := auth.db.QueryRow(`SELECT status, port FROM deployments WHERE id = ?`, result.ID).Scan(&savedStatus, &savedPort); err != nil {
		t.Fatalf("read saved deployment: %v", err)
	}
	if savedStatus != "running" || savedPort != 3000 {
		t.Fatalf("saved deployment status/port = %q/%d, want running/3000", savedStatus, savedPort)
	}
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/deployments/"+strconv.FormatInt(result.ID, 10), nil)
	detailRequest.AddCookie(cookie)
	detailResponse := httptest.NewRecorder()
	mux.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("deployment detail status = %d, body = %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail deployment
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode deployment detail: %v", err)
	}
	if detail.Status != "running" || detail.LatestRunID != result.LatestRunID {
		t.Fatalf("unexpected deployment detail: %+v", detail)
	}
	historyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/deployments/"+strconv.FormatInt(result.ID, 10)+"/history", nil)
	historyRequest.AddCookie(cookie)
	historyResponse := httptest.NewRecorder()
	mux.ServeHTTP(historyResponse, historyRequest)
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("deployment history status = %d, body = %s", historyResponse.Code, historyResponse.Body.String())
	}
	var history []deploymentRun
	if err := json.Unmarshal(historyResponse.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode deployment history: %v", err)
	}
	if len(history) != 1 || history[0].Status != "running" {
		t.Fatalf("unexpected deployment history: %+v", history)
	}
}

func TestAuthServiceMigratesDeploymentPort(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE deployments (
		id INTEGER PRIMARY KEY,
		workspace_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		repository_url TEXT NOT NULL,
		branch TEXT NOT NULL DEFAULT '',
		dockerfile TEXT NOT NULL,
		status TEXT NOT NULL,
		url TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE (workspace_id, name)
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create legacy deployments table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	t.Setenv("DATABASE_PATH", databasePath)
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth service: %v", err)
	}
	defer auth.db.Close()
	var portColumnCount int
	if err := auth.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('deployments') WHERE name = 'port'`).Scan(&portColumnCount); err != nil {
		t.Fatalf("check deployment port column: %v", err)
	}
	if portColumnCount != 1 {
		t.Fatalf("deployment port column count = %d, want 1", portColumnCount)
	}
}

func TestGitHubRepositoryValidation(t *testing.T) {
	valid := []string{
		"https://github.com/acme/storefront",
		"https://github.com/acme/storefront.git",
	}
	for _, repository := range valid {
		if !validPublicGitHubRepository(repository) {
			t.Errorf("expected valid GitHub repository %q", repository)
		}
	}
	invalid := []string{
		"http://github.com/acme/storefront",
		"https://github.com.evil.test/acme/storefront",
		"https://github.com/acme/storefront?redirect=elsewhere",
		"https://gitlab.com/acme/storefront",
	}
	for _, repository := range invalid {
		if validPublicGitHubRepository(repository) {
			t.Errorf("expected invalid GitHub repository %q", repository)
		}
	}
}

func TestDeploymentAPIEnforcesTokenScope(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	service := &deploymentService{db: auth.db, auth: auth, provisioner: &testProvisioner{}}
	service.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)

	createTokenRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewBufferString(
		`{"name":"Read only","scopes":["deployments:read"]}`))
	createTokenRequest.Host = "crelan.test"
	createTokenRequest.Header.Set("Origin", "http://crelan.test")
	createTokenRequest.Header.Set("Content-Type", "application/json")
	createTokenRequest.AddCookie(cookie)
	createTokenResponse := httptest.NewRecorder()
	mux.ServeHTTP(createTokenResponse, createTokenRequest)
	if createTokenResponse.Code != http.StatusCreated {
		t.Fatalf("create read token status = %d, body = %s", createTokenResponse.Code, createTokenResponse.Body.String())
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(createTokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatalf("decode read token: %v", err)
	}

	deployRequest := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", bytes.NewBufferString(
		`{"name":"storefront","repository_url":"https://github.com/acme/storefront"}`))
	deployRequest.Header.Set("Authorization", "Bearer "+token.Token)
	deployRequest.Header.Set("Content-Type", "application/json")
	deployResponse := httptest.NewRecorder()
	mux.ServeHTTP(deployResponse, deployRequest)
	if deployResponse.Code != http.StatusForbidden {
		t.Fatalf("read-only deployment create status = %d, want %d", deployResponse.Code, http.StatusForbidden)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/deployments", nil)
	listRequest.Header.Set("Authorization", "Bearer "+token.Token)
	listResponse := httptest.NewRecorder()
	mux.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("read-only deployment list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestCancelStopsActiveDeploymentWorker(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)
	provider := &blockingDeploymentProvisioner{started: make(chan struct{})}
	service := &deploymentService{db: auth.db, auth: auth, provisioner: provider}
	service.registerRoutes(mux)
	identity := httptest.NewRequest(http.MethodGet, "/", nil)
	identity.AddCookie(cookie)
	_, workspaceID, err := auth.sessionWorkspace(identity)
	if err != nil {
		t.Fatalf("resolve session workspace: %v", err)
	}
	deploymentID, runID, _, err := service.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "storefront", RepositoryURL: "https://github.com/acme/storefront",
	})
	if err != nil {
		t.Fatalf("queue deployment: %v", err)
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- service.processNextRun(context.Background()) }()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("deployment worker did not start")
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/deployments/"+strconv.FormatInt(deploymentID, 10)+"/cancel", nil)
	request.Host = "crelan.test"
	request.Header.Set("Origin", "http://crelan.test")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case err := <-workerResult:
		if err != nil {
			t.Fatalf("worker returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled deployment worker did not stop")
	}
	var runStatus, deploymentStatus string
	if err := auth.db.QueryRow(`SELECT status FROM deployment_runs WHERE id = ?`, runID).Scan(&runStatus); err != nil {
		t.Fatalf("read run status: %v", err)
	}
	if err := auth.db.QueryRow(`SELECT status FROM deployments WHERE id = ?`, deploymentID).Scan(&deploymentStatus); err != nil {
		t.Fatalf("read deployment status: %v", err)
	}
	if runStatus != "cancelled" || deploymentStatus != "cancelled" {
		t.Fatalf("cancel statuses = run %q/deployment %q", runStatus, deploymentStatus)
	}
}

func TestDeploymentWorkerRecoversInterruptedRun(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)
	identity := httptest.NewRequest(http.MethodGet, "/", nil)
	identity.AddCookie(cookie)
	_, workspaceID, err := auth.sessionWorkspace(identity)
	if err != nil {
		t.Fatalf("resolve session workspace: %v", err)
	}
	service := &deploymentService{db: auth.db}
	deploymentID, runID, _, err := service.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "storefront", RepositoryURL: "https://github.com/acme/storefront",
	})
	if err != nil {
		t.Fatalf("queue deployment: %v", err)
	}
	if _, err := auth.db.Exec(`UPDATE deployment_runs SET status = 'building' WHERE id = ?`, runID); err != nil {
		t.Fatalf("mark run interrupted: %v", err)
	}
	if _, err := auth.db.Exec(`UPDATE deployments SET status = 'building' WHERE id = ?`, deploymentID); err != nil {
		t.Fatalf("mark deployment interrupted: %v", err)
	}
	if err := service.recoverInterruptedRuns(context.Background()); err != nil {
		t.Fatalf("recover interrupted runs: %v", err)
	}
	var runStatus, deploymentStatus string
	if err := auth.db.QueryRow(`SELECT status FROM deployment_runs WHERE id = ?`, runID).Scan(&runStatus); err != nil {
		t.Fatalf("read recovered run: %v", err)
	}
	if err := auth.db.QueryRow(`SELECT status FROM deployments WHERE id = ?`, deploymentID).Scan(&deploymentStatus); err != nil {
		t.Fatalf("read recovered deployment: %v", err)
	}
	if runStatus != "queued" || deploymentStatus != "queued" {
		t.Fatalf("recovered statuses = run %q/deployment %q, want queued/queued", runStatus, deploymentStatus)
	}
}

func TestCustomDomainRequiresAndVerifiesDNSOwnership(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	t.Setenv("CRELAN_CERT_MANAGER_ISSUER", "letsencrypt")
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)
	identity := httptest.NewRequest(http.MethodGet, "/", nil)
	identity.AddCookie(cookie)
	_, workspaceID, err := auth.sessionWorkspace(identity)
	if err != nil {
		t.Fatalf("resolve session workspace: %v", err)
	}
	provider := &customDomainTestProvisioner{}
	service := &deploymentService{db: auth.db, auth: auth, provisioner: provider}
	service.registerRoutes(mux)
	deploymentID, _, _, err := service.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "storefront", RepositoryURL: "https://github.com/acme/storefront",
	})
	if err != nil {
		t.Fatalf("queue app: %v", err)
	}
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/deployments/"+strconv.FormatInt(deploymentID, 10)+"/domains",
		bytes.NewBufferString(`{"host":"app.example.test"}`))
	createRequest.Host = "crelan.test"
	createRequest.Header.Set("Origin", "http://crelan.test")
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.AddCookie(cookie)
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create domain status = %d, body=%s", createResponse.Code, createResponse.Body.String())
	}
	var domain customDomain
	if err := json.Unmarshal(createResponse.Body.Bytes(), &domain); err != nil {
		t.Fatalf("decode custom domain response: %v", err)
	}
	service.lookupTXT = func(_ context.Context, name string) ([]string, error) {
		if name != "_crelan-verify.app.example.test" {
			t.Fatalf("TXT lookup name = %q", name)
		}
		return []string{domain.TXTValue}, nil
	}
	verifyRequest := httptest.NewRequest(http.MethodPost,
		"/api/v1/deployments/"+strconv.FormatInt(deploymentID, 10)+"/domains/"+strconv.FormatInt(domain.ID, 10)+"/verify", nil)
	verifyRequest.Host = "crelan.test"
	verifyRequest.Header.Set("Origin", "http://crelan.test")
	verifyRequest.AddCookie(cookie)
	verifyResponse := httptest.NewRecorder()
	mux.ServeHTTP(verifyResponse, verifyRequest)
	if verifyResponse.Code != http.StatusOK || provider.host != domain.Host {
		t.Fatalf("verify status=%d body=%s provisioned host=%q", verifyResponse.Code, verifyResponse.Body.String(), provider.host)
	}
	var status string
	if err := auth.db.QueryRow(`SELECT status FROM custom_domains WHERE id = ?`, domain.ID).Scan(&status); err != nil {
		t.Fatalf("read verified domain: %v", err)
	}
	if status != "active" {
		t.Fatalf("domain status = %q, want active", status)
	}
}

func TestWorkspaceLimitsAndDeploymentIsolation(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "crelan.db"))
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("initialize auth: %v", err)
	}
	defer auth.db.Close()
	mux := http.NewServeMux()
	auth.registerRoutes(mux)
	service := &deploymentService{db: auth.db, auth: auth, provisioner: &testProvisioner{}}
	service.registerRoutes(mux)
	cookie := createTestSession(t, auth, mux)
	identity := httptest.NewRequest(http.MethodGet, "/", nil)
	identity.AddCookie(cookie)
	_, workspaceID, err := auth.sessionWorkspace(identity)
	if err != nil {
		t.Fatalf("resolve test workspace: %v", err)
	}
	if _, err := auth.db.Exec(`INSERT INTO workspace_limits (workspace_id, max_apps, max_concurrency, max_cpu_millicores, max_memory_mb, updated_at) VALUES (?, 1, 1, 2000, 4096, ?)`,
		workspaceID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("set workspace limits: %v", err)
	}
	createdID, _, _, err := service.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "first", RepositoryURL: "https://github.com/acme/first",
	})
	if err != nil {
		t.Fatalf("queue first app: %v", err)
	}
	if _, _, _, err := service.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "second", RepositoryURL: "https://github.com/acme/second",
	}); !errors.Is(err, errWorkspaceAppLimit) {
		t.Fatalf("second app error = %v, want app-limit error", err)
	}
	if _, _, _, err := service.queueDeployment(context.Background(), workspaceID, DeploymentRequest{
		Name: "first", RepositoryURL: "https://github.com/acme/first", CPUMillicores: 2500,
	}); !errors.Is(err, errWorkspaceResourceLimit) {
		t.Fatalf("over-limit app error = %v, want resource-limit error", err)
	}

	other, err := auth.createAccount(context.Background(), "Other", "other@example.test", "test-hash")
	if err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	otherDeploymentID, _, _, err := service.queueDeployment(context.Background(), other.WorkspaceID, DeploymentRequest{
		Name: "private-app", RepositoryURL: "https://github.com/acme/private-app",
	})
	if err != nil {
		t.Fatalf("queue other workspace app: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/deployments/"+strconv.FormatInt(otherDeploymentID, 10), nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace detail status = %d, want 404", response.Code)
	}
	if createdID == otherDeploymentID {
		t.Fatalf("workspace deployment IDs unexpectedly equal: %d", createdID)
	}
}
