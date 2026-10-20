package main

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type githubAppService struct {
	db            *sql.DB
	auth          *authService
	deployments   *deploymentService
	client        *http.Client
	appID         string
	slug          string
	privateKey    *rsa.PrivateKey
	webhookSecret []byte
	configured    bool
}

func newGitHubAppService(db *sql.DB, auth *authService) (*githubAppService, error) {
	appID := strings.TrimSpace(getenv("CRELAN_GITHUB_APP_ID"))
	slug := strings.TrimSpace(getenv("CRELAN_GITHUB_APP_SLUG"))
	keyData := strings.ReplaceAll(strings.TrimSpace(getenv("CRELAN_GITHUB_APP_PRIVATE_KEY")), `\n`, "\n")
	webhookSecret := strings.TrimSpace(getenv("CRELAN_GITHUB_APP_WEBHOOK_SECRET"))
	service := &githubAppService{
		db: db, auth: auth, client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(request *http.Request, _ []*http.Request) error {
				if request.URL.Host != "api.github.com" {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		appID: appID, slug: slug, webhookSecret: []byte(webhookSecret),
	}
	if appID == "" && slug == "" && keyData == "" && webhookSecret == "" {
		return service, nil
	}
	if appID == "" || slug == "" || keyData == "" || webhookSecret == "" {
		return nil, errors.New("CRELAN_GITHUB_APP_ID, CRELAN_GITHUB_APP_SLUG, CRELAN_GITHUB_APP_PRIVATE_KEY, and CRELAN_GITHUB_APP_WEBHOOK_SECRET must be configured together")
	}
	if _, err := strconv.ParseInt(appID, 10, 64); err != nil {
		return nil, errors.New("CRELAN_GITHUB_APP_ID must be a numeric GitHub App ID")
	}
	if !validGitHubPathPart(slug) {
		return nil, errors.New("CRELAN_GITHUB_APP_SLUG must be a valid GitHub App slug")
	}
	block, _ := pem.Decode([]byte(keyData))
	if block == nil {
		return nil, errors.New("CRELAN_GITHUB_APP_PRIVATE_KEY must contain a PEM-encoded RSA private key")
	}
	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = parsed
	} else if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("CRELAN_GITHUB_APP_PRIVATE_KEY must be an RSA private key")
		}
	} else {
		return nil, errors.New("could not parse CRELAN_GITHUB_APP_PRIVATE_KEY")
	}
	service.privateKey = key
	service.configured = true
	return service, nil
}

func (s *githubAppService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/github/install", s.install)
	mux.HandleFunc("GET /auth/github-app/callback", s.callback)
	mux.HandleFunc("GET /api/v1/github/installation", s.installationStatus)
	mux.HandleFunc("GET /api/v1/github/repositories", s.repositories)
	mux.HandleFunc("POST /webhooks/github", s.webhook)
}

func (s *githubAppService) install(w http.ResponseWriter, r *http.Request) {
	if !s.configured {
		http.Error(w, "GitHub App integration is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM github_app_states WHERE expires_at <= ?`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		http.Error(w, "Could not start GitHub installation", http.StatusInternalServerError)
		return
	}
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "Could not start GitHub installation", http.StatusInternalServerError)
		return
	}
	expires := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(r.Context(), `
INSERT INTO github_app_states (state_hash, user_id, workspace_id, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(state), userID, workspaceID, expires); err != nil {
		http.Error(w, "Could not start GitHub installation", http.StatusInternalServerError)
		return
	}
	target := "https://github.com/apps/" + url.PathEscape(s.slug) + "/installations/new?state=" + url.QueryEscape(state)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *githubAppService) callback(w http.ResponseWriter, r *http.Request) {
	if !s.configured {
		http.Error(w, "GitHub App integration is not configured", http.StatusServiceUnavailable)
		return
	}
	state := r.URL.Query().Get("state")
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if state == "" || err != nil || installationID < 1 {
		http.Redirect(w, r, "/dashboard?github=invalid_callback#applications", http.StatusSeeOther)
		return
	}
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var expectedUser, expectedWorkspace int64
	var expiresAt string
	err = s.db.QueryRowContext(r.Context(), `
SELECT user_id, workspace_id, expires_at FROM github_app_states WHERE state_hash = ?`, hashToken(state)).
		Scan(&expectedUser, &expectedWorkspace, &expiresAt)
	if err != nil || expectedUser != userID || expectedWorkspace != workspaceID {
		http.Redirect(w, r, "/dashboard?github=invalid_state#applications", http.StatusSeeOther)
		return
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !expiry.After(time.Now()) {
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM github_app_states WHERE state_hash = ?`, hashToken(state))
		http.Redirect(w, r, "/dashboard?github=expired_state#applications", http.StatusSeeOther)
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM github_app_states WHERE state_hash = ?`, hashToken(state))
	installation, err := s.getInstallation(r.Context(), installationID)
	if err != nil {
		http.Redirect(w, r, "/dashboard?github=verification_failed#applications", http.StatusSeeOther)
		return
	}
	_, err = s.db.ExecContext(r.Context(), `
INSERT INTO github_app_installations (workspace_id, installation_id, account_login, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(workspace_id) DO UPDATE SET
	installation_id = excluded.installation_id,
	account_login = excluded.account_login,
	created_at = excluded.created_at`,
		workspaceID, installationID, installation.Account.Login, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		http.Redirect(w, r, "/dashboard?github=save_failed#applications", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard?github=connected#applications", http.StatusSeeOther)
}

func (s *githubAppService) installationStatus(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in to view GitHub installation")
		return
	}
	var account string
	err = s.db.QueryRowContext(r.Context(), `SELECT account_login FROM github_app_installations WHERE workspace_id = ?`, workspaceID).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"configured": s.configured, "connected": false})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load GitHub installation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": s.configured, "connected": true, "account": account})
}

func (s *githubAppService) repositories(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in to list GitHub repositories")
		return
	}
	if !s.configured {
		writeError(w, http.StatusServiceUnavailable, "GitHub App integration is not configured")
		return
	}
	token, err := s.installationToken(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not access the connected GitHub installation")
		return
	}
	if token == "" {
		writeJSON(w, http.StatusOK, []githubRepository{})
		return
	}
	var result struct {
		Repositories []githubRepository `json:"repositories"`
	}
	if err := s.githubRequest(r.Context(), http.MethodGet, "https://api.github.com/installation/repositories?per_page=100", token, nil, &result); err != nil {
		writeError(w, http.StatusBadGateway, "Could not list repositories for this workspace")
		return
	}
	writeJSON(w, http.StatusOK, result.Repositories)
}

func (s *githubAppService) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.configured {
		writeError(w, http.StatusServiceUnavailable, "GitHub App integration is not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20+1))
	if err != nil || len(body) > 2<<20 {
		writeError(w, http.StatusBadRequest, "Invalid webhook payload")
		return
	}
	rawSignature := r.Header.Get("X-Hub-Signature-256")
	signature, err := hex.DecodeString(strings.TrimPrefix(rawSignature, "sha256="))
	if !strings.HasPrefix(rawSignature, "sha256=") || err != nil || !s.validWebhookSignature(body, signature) {
		writeError(w, http.StatusUnauthorized, "Invalid webhook signature")
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		writeError(w, http.StatusBadRequest, "Missing GitHub delivery identifier")
		return
	}
	var alreadyReceived int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM github_webhook_deliveries WHERE delivery_id = ?`, deliveryID).Scan(&alreadyReceived); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not process GitHub webhook")
		return
	}
	if alreadyReceived > 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM github_webhook_deliveries WHERE received_at < ?`,
		time.Now().UTC().Add(-30*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not process GitHub webhook")
		return
	}
	if event == "installation" {
		if err := s.handleInstallationEvent(r.Context(), body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid GitHub installation event")
			return
		}
	} else if event == "push" {
		if err := s.handlePushEvent(r.Context(), body); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not queue GitHub push deployment")
			return
		}
	}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO github_webhook_deliveries (delivery_id, received_at) VALUES (?, ?)`,
		deliveryID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not record GitHub webhook")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *githubAppService) handleInstallationEvent(ctx context.Context, body []byte) error {
	var event struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return err
	}
	if event.Action == "deleted" && event.Installation.ID > 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM github_app_installations WHERE installation_id = ?`, event.Installation.ID)
		return err
	}
	return nil
}

func (s *githubAppService) handlePushEvent(ctx context.Context, body []byte) error {
	var event struct {
		Ref          string `json:"ref"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			CloneURL      string `json:"clone_url"`
			DefaultBranch string `json:"default_branch"`
			FullName      string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return err
	}
	branch := strings.TrimPrefix(event.Ref, "refs/heads/")
	if event.Installation.ID < 1 || branch == "" || !validGitHubRepository(event.Repository.CloneURL) {
		return errors.New("push event is missing installation, branch, or repository")
	}
	var workspaceID int64
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM github_app_installations WHERE installation_id = ?`, event.Installation.ID).Scan(&workspaceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT name, repository_url, branch, dockerfile, port
FROM deployments WHERE workspace_id = ? AND status != 'deleted'`, workspaceID)
	if err != nil {
		return err
	}
	type deployRequest struct {
		name, repository, branch, dockerfile string
		port                                 int
	}
	var matches []deployRequest
	for rows.Next() {
		var item deployRequest
		if err := rows.Scan(&item.name, &item.repository, &item.branch, &item.dockerfile, &item.port); err != nil {
			rows.Close()
			return err
		}
		if githubRepositoryKey(item.repository) == githubRepositoryKey(event.Repository.CloneURL) &&
			(item.branch == branch || item.branch == "" && branch == event.Repository.DefaultBranch) {
			matches = append(matches, item)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range matches {
		_, _, _, err := s.deployments.queueDeployment(ctx, workspaceID, DeploymentRequest{
			Name: item.name, RepositoryURL: item.repository, Branch: branch,
			Dockerfile: item.dockerfile, Port: item.port,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *githubAppService) installationToken(ctx context.Context, workspaceID int64) (string, error) {
	if !s.configured {
		return "", nil
	}
	var installationID int64
	err := s.db.QueryRowContext(ctx, `SELECT installation_id FROM github_app_installations WHERE workspace_id = ?`, workspaceID).Scan(&installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read GitHub installation: %w", err)
	}
	jwt, err := s.appJWT()
	if err != nil {
		return "", err
	}
	var response struct {
		Token string `json:"token"`
	}
	endpoint := "https://api.github.com/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	if err := s.githubRequest(ctx, http.MethodPost, endpoint, jwt, map[string]any{}, &response); err != nil {
		return "", err
	}
	if response.Token == "" {
		return "", errors.New("GitHub returned an empty installation token")
	}
	return response.Token, nil
}

func (s *githubAppService) getInstallation(ctx context.Context, installationID int64) (githubInstallation, error) {
	jwt, err := s.appJWT()
	if err != nil {
		return githubInstallation{}, err
	}
	var installation githubInstallation
	endpoint := "https://api.github.com/app/installations/" + strconv.FormatInt(installationID, 10)
	if err := s.githubRequest(ctx, http.MethodGet, endpoint, jwt, nil, &installation); err != nil {
		return githubInstallation{}, err
	}
	if installation.ID != installationID || installation.Account.Login == "" {
		return githubInstallation{}, errors.New("GitHub installation response is invalid")
	}
	return installation, nil
}

func (s *githubAppService) appJWT() (string, error) {
	now := time.Now().Unix()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{"iat": now - 60, "exp": now + 8*60, "iss": s.appID})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign GitHub App JWT: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *githubAppService) githubRequest(ctx context.Context, method, endpoint, token string, payload, result any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "Crelan")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("request GitHub API: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read GitHub API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("decode GitHub API response: %w", err)
		}
	}
	return nil
}

func (s *githubAppService) validWebhookSignature(body, signature []byte) bool {
	if len(s.webhookSecret) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, s.webhookSecret)
	_, _ = mac.Write(body)
	return hmac.Equal(signature, mac.Sum(nil))
}

type githubInstallationAccount struct {
	Login string `json:"login"`
}

type githubInstallation struct {
	ID      int64                     `json:"id"`
	Account githubInstallationAccount `json:"account"`
}

type githubRepository struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
}

func (a *authService) sessionWorkspace(r *http.Request) (int64, int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, 0, errors.New("session cookie missing")
	}
	var userID int64
	var expiresAt string
	err = a.db.QueryRowContext(r.Context(), `SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`,
		hashToken(cookie.Value)).Scan(&userID, &expiresAt)
	if err != nil {
		return 0, 0, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !expiry.After(time.Now()) {
		return 0, 0, errors.New("session expired")
	}
	var workspaceID int64
	err = a.db.QueryRowContext(r.Context(), `
SELECT workspace_id FROM workspace_members WHERE user_id = ?
ORDER BY CASE role WHEN 'owner' THEN 0 ELSE 1 END, workspace_id LIMIT 1`, userID).Scan(&workspaceID)
	return userID, workspaceID, err
}

func githubRepositoryKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/"), "/")
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(parts[0] + "/" + parts[1])
}
