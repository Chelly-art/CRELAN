package main

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	assets "crelan"
	_ "modernc.org/sqlite"
)

const (
	sessionCookieName  = "crelan_session"
	sessionLifetime    = 7 * 24 * time.Hour
	passwordIterations = 310_000
)

type authService struct {
	db               *sql.DB
	httpClient       *http.Client
	githubClientID   string
	githubSecret     string
	cookieSecure     bool
	demoLoginEnabled bool
}

type authUser struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	WorkspaceID int64  `json:"workspace_id"`
	Workspace   string `json:"workspace"`
}

func newAuthService() (*authService, error) {
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = ":memory:"
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable database foreign keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database schema: %w", err)
	}
	if err := ensureDeploymentColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate deployment port: %w", err)
	}
	if databasePath != ":memory:" {
		if err := os.Chmod(databasePath, 0o600); err != nil {
			db.Close()
			return nil, fmt.Errorf("secure database file permissions: %w", err)
		}
	}

	return &authService{
		db:               db,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		githubClientID:   os.Getenv("GITHUB_CLIENT_ID"),
		githubSecret:     os.Getenv("GITHUB_CLIENT_SECRET"),
		cookieSecure:     strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true"),
		demoLoginEnabled: strings.EqualFold(os.Getenv("CRELAN_DEMO_LOGIN"), "true"),
	}, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspaces (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_members (
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL,
	PRIMARY KEY (workspace_id, user_id)
);
CREATE TABLE IF NOT EXISTS oauth_identities (
	provider TEXT NOT NULL,
	provider_user_id TEXT NOT NULL,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (provider, provider_user_id)
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS api_tokens (
	id INTEGER PRIMARY KEY,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	token_prefix TEXT NOT NULL,
	scopes TEXT NOT NULL,
	expires_at TEXT,
	revoked_at TEXT,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS api_tokens_workspace ON api_tokens(workspace_id, created_at);
CREATE TABLE IF NOT EXISTS deployments (
	id INTEGER PRIMARY KEY,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	repository_url TEXT NOT NULL,
	branch TEXT NOT NULL DEFAULT '',
	cluster_context TEXT NOT NULL DEFAULT '',
	dockerfile TEXT NOT NULL,
	port INTEGER NOT NULL DEFAULT 8080,
	cpu_millicores INTEGER NOT NULL DEFAULT 1000,
	memory_mb INTEGER NOT NULL DEFAULT 512,
	status TEXT NOT NULL,
	status_message TEXT NOT NULL DEFAULT '',
	latest_run_id INTEGER,
	url TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (workspace_id, name)
);
CREATE INDEX IF NOT EXISTS deployments_workspace ON deployments(workspace_id, created_at);
CREATE TABLE IF NOT EXISTS deployment_runs (
	id INTEGER PRIMARY KEY,
	deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	status TEXT NOT NULL,
	message TEXT NOT NULL DEFAULT '',
	request_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT
);
CREATE INDEX IF NOT EXISTS deployment_runs_deployment ON deployment_runs(deployment_id, id DESC);
CREATE INDEX IF NOT EXISTS deployment_runs_queue ON deployment_runs(status, id);
CREATE TABLE IF NOT EXISTS github_app_installations (
	workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
	installation_id INTEGER NOT NULL UNIQUE,
	account_login TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS github_app_states (
	state_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
	delivery_id TEXT PRIMARY KEY,
	received_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_limits (
	workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
	max_apps INTEGER NOT NULL DEFAULT 5,
	max_concurrency INTEGER NOT NULL DEFAULT 1,
	max_cpu_millicores INTEGER NOT NULL DEFAULT 2000,
	max_memory_mb INTEGER NOT NULL DEFAULT 4096,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER PRIMARY KEY,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	user_id INTEGER,
	action TEXT NOT NULL,
	details TEXT,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_events_workspace ON audit_events(workspace_id, id DESC);
CREATE TABLE IF NOT EXISTS custom_domains (
	id INTEGER PRIMARY KEY,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
	host TEXT NOT NULL UNIQUE,
	verification_token TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	created_at TEXT NOT NULL,
	verified_at TEXT
);
`

func ensureDeploymentColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(deployments)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	migrations := []struct {
		name, statement string
	}{
		{"port", `ALTER TABLE deployments ADD COLUMN port INTEGER NOT NULL DEFAULT 8080`},
		{"cluster_context", `ALTER TABLE deployments ADD COLUMN cluster_context TEXT NOT NULL DEFAULT ''`},
		{"status_message", `ALTER TABLE deployments ADD COLUMN status_message TEXT NOT NULL DEFAULT ''`},
		{"latest_run_id", `ALTER TABLE deployments ADD COLUMN latest_run_id INTEGER`},
		{"cpu_millicores", `ALTER TABLE deployments ADD COLUMN cpu_millicores INTEGER NOT NULL DEFAULT 1000`},
		{"memory_mb", `ALTER TABLE deployments ADD COLUMN memory_mb INTEGER NOT NULL DEFAULT 512`},
	}
	for _, migration := range migrations {
		if columns[migration.name] {
			continue
		}
		if _, err := db.Exec(migration.statement); err != nil {
			return fmt.Errorf("add deployments.%s: %w", migration.name, err)
		}
	}
	return nil
}

func (a *authService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/signup", a.signup)
	mux.HandleFunc("POST /auth/login", a.login)
	mux.HandleFunc("POST /auth/logout", a.logout)
	mux.HandleFunc("GET /auth/github", a.githubStart)
	mux.HandleFunc("GET /auth/github/callback", a.githubCallback)
	mux.HandleFunc("GET /api/v1/me", a.currentUser)
	mux.HandleFunc("GET /api/v1/tokens", a.listTokens)
	mux.HandleFunc("POST /api/v1/tokens", a.createToken)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", a.revokeToken)
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("GET /signup", a.signupPage)
	mux.HandleFunc("GET /", a.landingPage)
	mux.HandleFunc("GET /dashboard", a.dashboardPage)
}

func (a *authService) signup(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter a valid name, email, and password")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if len(input.Name) < 1 || len(input.Name) > 80 {
		writeError(w, http.StatusBadRequest, "Name must be between 1 and 80 characters")
		return
	}
	if len(input.Email) > 254 || !validEmail(input.Email) {
		writeError(w, http.StatusBadRequest, "Enter a valid email address")
		return
	}
	if len(input.Password) < 12 || len(input.Password) > 128 {
		writeError(w, http.StatusBadRequest, "Password must be between 12 and 128 characters")
		return
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		log.Printf("hash signup password: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create account")
		return
	}

	user, err := a.createAccount(r.Context(), input.Name, input.Email, passwordHash)
	if err != nil {
		if errors.Is(err, errEmailTaken) {
			writeError(w, http.StatusConflict, "An account with that email already exists")
			return
		}
		log.Printf("create account: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create account")
		return
	}
	if err := a.startSession(w, r, user.ID); err != nil {
		log.Printf("start signup session: %v", err)
		writeError(w, http.StatusInternalServerError, "Account created, but sign-in failed. Please sign in.")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

var errEmailTaken = errors.New("email already exists")

func (a *authService) createAccount(ctx context.Context, name, email, passwordHash string) (authUser, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return authUser{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		name, email, passwordHash, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return authUser{}, errEmailTaken
		}
		return authUser{}, err
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return authUser{}, err
	}
	workspaceName := name + "'s workspace"
	result, err = tx.ExecContext(ctx,
		`INSERT INTO workspaces (name, created_at) VALUES (?, ?)`, workspaceName, now)
	if err != nil {
		return authUser{}, err
	}
	workspaceID, err := result.LastInsertId()
	if err != nil {
		return authUser{}, err
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, 'owner')`,
		workspaceID, userID); err != nil {
		return authUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return authUser{}, err
	}
	return authUser{ID: userID, Name: name, Email: email, WorkspaceID: workspaceID, Workspace: workspaceName}, nil
}

func (a *authService) login(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		Identity string `json:"identity"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter a username or email and password")
		return
	}
	// This demo has no account verification. Keep a single workspace identity;
	// the submitted username and password are deliberately not persisted.
	userID, err := a.ensureDemoAccount(r.Context())
	if err != nil {
		log.Printf("initialize demo workspace: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not sign in")
		return
	}
	a.finishLogin(w, r, userID)
}

func (a *authService) finishLogin(w http.ResponseWriter, r *http.Request, userID int64) {
	user, err := a.userWithWorkspace(r.Context(), userID)
	if err != nil {
		log.Printf("load signed-in user workspace: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not sign in")
		return
	}
	if err := a.startSession(w, r, userID); err != nil {
		log.Printf("start login session: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not sign in")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (a *authService) ensureDemoAccount(ctx context.Context) (int64, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var userID int64
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM oauth_identities WHERE provider = 'demo' AND provider_user_id = 'admin'`).
		Scan(&userID)
	if err == nil {
		return userID, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		`INSERT INTO users (name, email, created_at) VALUES ('Admin', 'admin@crelan.local', ?)`, now)
	if err != nil {
		return 0, err
	}
	userID, err = result.LastInsertId()
	if err != nil {
		return 0, err
	}
	result, err = tx.ExecContext(ctx,
		`INSERT INTO workspaces (name, created_at) VALUES ('Admin workspace', ?)`, now)
	if err != nil {
		return 0, err
	}
	workspaceID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, 'owner')`,
		workspaceID, userID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO oauth_identities (provider, provider_user_id, user_id) VALUES ('demo', 'admin', ?)`,
		userID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return userID, nil
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *authService) currentUser(w http.ResponseWriter, r *http.Request) {
	userID, err := a.sessionUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	user, err := a.userWithWorkspace(r.Context(), userID)
	if err != nil {
		log.Printf("load current user: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load account")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (a *authService) userWithWorkspace(ctx context.Context, userID int64) (authUser, error) {
	var user authUser
	err := a.db.QueryRowContext(ctx, `
SELECT u.id, u.name, u.email, w.id, w.name
FROM users u JOIN workspace_members m ON m.user_id = u.id
JOIN workspaces w ON w.id = m.workspace_id
WHERE u.id = ?
ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, w.id
LIMIT 1`, userID).Scan(&user.ID, &user.Name, &user.Email, &user.WorkspaceID, &user.Workspace)
	return user, err
}

func (a *authService) logout(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_, err = a.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE token_hash = ?`, hashToken(cookie.Value))
		if err != nil {
			log.Printf("delete session: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not sign out")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: a.secureRequest(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed_out"})
}

func (a *authService) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	expires := now.Add(sessionLifetime)
	_, err = a.db.ExecContext(r.Context(),
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, _ = a.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE expires_at < ?`, now.Format(time.RFC3339Nano))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: a.secureRequest(r), SameSite: http.SameSiteLaxMode,
		Expires: expires, MaxAge: int(sessionLifetime.Seconds()),
	})
	return nil
}

func (a *authService) sessionUserID(r *http.Request) (int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, errors.New("session cookie missing")
	}
	var userID int64
	var expiresAt string
	err = a.db.QueryRowContext(r.Context(),
		`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, hashToken(cookie.Value)).
		Scan(&userID, &expiresAt)
	if err != nil {
		return 0, errors.New("session not found")
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !expires.After(time.Now()) {
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE token_hash = ?`, hashToken(cookie.Value))
		return 0, errors.New("session expired")
	}
	return userID, nil
}

func (a *authService) githubStart(w http.ResponseWriter, r *http.Request) {
	if a.githubClientID == "" || a.githubSecret == "" {
		http.Redirect(w, r, "/login?error=github_not_configured", http.StatusSeeOther)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		log.Printf("create OAuth state: %v", err)
		http.Error(w, "Could not start GitHub sign-in", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "crelan_oauth_state", Value: state, Path: "/auth/github/callback",
		HttpOnly: true, Secure: a.secureRequest(r), SameSite: http.SameSiteLaxMode,
		MaxAge: 600,
	})
	callback := callbackURL(r)
	params := url.Values{
		"client_id":    {a.githubClientID},
		"redirect_uri": {callback},
		"scope":        {"read:user user:email"},
		"state":        {state},
	}
	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+params.Encode(), http.StatusSeeOther)
}

func (a *authService) githubCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("crelan_oauth_state")
	if err != nil || stateCookie.Value == "" || r.URL.Query().Get("state") == "" ||
		subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "Invalid GitHub sign-in state. Please try again.", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "crelan_oauth_state", Value: "", Path: "/auth/github/callback",
		HttpOnly: true, Secure: a.secureRequest(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/login?error=github_cancelled", http.StatusSeeOther)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/login?error=github_failed", http.StatusSeeOther)
		return
	}

	accessToken, err := a.exchangeGithubCode(r.Context(), code, callbackURL(r))
	if err != nil {
		log.Printf("exchange GitHub OAuth code: %v", err)
		http.Redirect(w, r, "/login?error=github_failed", http.StatusSeeOther)
		return
	}
	githubUser, err := a.githubProfile(r.Context(), accessToken)
	if err != nil {
		log.Printf("read GitHub profile: %v", err)
		http.Redirect(w, r, "/login?error=github_failed", http.StatusSeeOther)
		return
	}
	userID, err := a.loginOrCreateGithubUser(r.Context(), githubUser)
	if err != nil {
		log.Printf("link GitHub account: %v", err)
		http.Redirect(w, r, "/login?error=github_failed", http.StatusSeeOther)
		return
	}
	if err := a.startSession(w, r, userID); err != nil {
		log.Printf("start GitHub session: %v", err)
		http.Error(w, "Could not complete sign-in", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

type githubProfile struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (a *authService) exchangeGithubCode(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{
		"client_id":     {a.githubClientID},
		"client_secret": {a.githubSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub token endpoint returned %s", resp.Status)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("GitHub token exchange failed: %s", result.Error)
	}
	return result.AccessToken, nil
}

func (a *authService) githubProfile(ctx context.Context, accessToken string) (githubProfile, error) {
	var profile githubProfile
	if err := a.githubGET(ctx, accessToken, "https://api.github.com/user", &profile); err != nil {
		return profile, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := a.githubGET(ctx, accessToken, "https://api.github.com/user/emails", &emails); err != nil {
		return profile, err
	}
	for _, email := range emails {
		if email.Primary && email.Verified {
			profile.Email = strings.ToLower(email.Email)
			break
		}
	}
	if profile.Email == "" || profile.ID == 0 {
		return profile, errors.New("GitHub account has no verified primary email")
	}
	if profile.Name == "" {
		profile.Name = profile.Login
	}
	return profile, nil
}

func (a *authService) githubGET(ctx context.Context, accessToken, endpoint string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Crelan")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
}

func (a *authService) loginOrCreateGithubUser(ctx context.Context, profile githubProfile) (int64, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const provider = "github"
	providerID := strconv.FormatInt(profile.ID, 10)
	var userID int64
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM oauth_identities WHERE provider = ? AND provider_user_id = ?`,
		provider, providerID).Scan(&userID)
	if err == nil {
		return userID, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ? COLLATE NOCASE`, profile.Email).Scan(&userID)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if errors.Is(err, sql.ErrNoRows) {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			name = profile.Login
		}
		result, insertErr := tx.ExecContext(ctx,
			`INSERT INTO users (name, email, created_at) VALUES (?, ?, ?)`, name, profile.Email, now)
		if insertErr != nil {
			return 0, insertErr
		}
		userID, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
		workspaceName := name + "'s workspace"
		result, err = tx.ExecContext(ctx,
			`INSERT INTO workspaces (name, created_at) VALUES (?, ?)`, workspaceName, now)
		if err != nil {
			return 0, err
		}
		workspaceID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, 'owner')`,
			workspaceID, userID); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO oauth_identities (provider, provider_user_id, user_id) VALUES (?, ?, ?)`,
		provider, providerID, userID); err != nil {
		return 0, err
	}
	return userID, tx.Commit()
}

func (a *authService) loginPage(w http.ResponseWriter, r *http.Request) {
	a.serveAuthPage(w, r)
}

func (a *authService) signupPage(w http.ResponseWriter, r *http.Request) {
	a.serveAuthPage(w, r)
}

func (a *authService) serveAuthPage(w http.ResponseWriter, r *http.Request) {
	if _, err := a.sessionUserID(r); err == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	page, err := assets.Web.ReadFile("web/auth.html")
	if err != nil {
		log.Printf("read auth page: %v", err)
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (a *authService) landingPage(w http.ResponseWriter, r *http.Request) {
	page, err := assets.Web.ReadFile("web/index.html")
	if err != nil {
		log.Printf("read landing page: %v", err)
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
}

func (a *authService) dashboardPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dashboard" {
		http.NotFound(w, r)
		return
	}
	if _, err := a.sessionUserID(r); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	page, err := assets.Web.ReadFile("web/dashboard.html")
	if err != nil {
		log.Printf("read dashboard: %v", err)
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "dashboard.html", time.Time{}, bytes.NewReader(page))
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && strings.Contains(email, "@")
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key[hash.Hash](sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 100_000 || iterations > 1_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual, err := pbkdf2.Key[hash.Hash](sha256.New, password, salt, iterations, len(expected))
	return err == nil && subtle.ConstantTimeCompare(actual, expected) == 1
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func callbackURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded == "https" {
		scheme = "https"
	}
	if configured := os.Getenv("GITHUB_CALLBACK_URL"); configured != "" {
		return configured
	}
	return scheme + "://" + r.Host + "/auth/github/callback"
}

func (a *authService) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host != r.Host {
		return false
	}
	if a.secureRequest(r) {
		return parsed.Scheme == "https"
	}
	return parsed.Scheme == "http"
}

func (a *authService) secureRequest(r *http.Request) bool {
	return a.cookieSecure || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("expected JSON")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
