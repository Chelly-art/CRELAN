package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const workspaceInviteLifetime = 7 * 24 * time.Hour

type workspaceService struct {
	auth *authService
}

func newWorkspaceService(auth *authService) (*workspaceService, error) {
	if err := ensureWorkspaceSessionSchema(auth.db); err != nil {
		return nil, fmt.Errorf("initialize workspace session and invite schema: %w", err)
	}
	return &workspaceService{auth: auth}, nil
}

func ensureWorkspaceSessionSchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	hasWorkspace := false
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "workspace_id" {
			hasWorkspace = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasWorkspace {
		if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN workspace_id INTEGER REFERENCES workspaces(id) ON DELETE SET NULL`); err != nil {
			return err
		}
	}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS workspace_invites (
	token_hash TEXT PRIMARY KEY,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	email TEXT NOT NULL COLLATE NOCASE,
	role TEXT NOT NULL,
	invited_by INTEGER NOT NULL REFERENCES users(id),
	expires_at TEXT NOT NULL,
	accepted_at TEXT,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS workspace_invites_pending ON workspace_invites(workspace_id, email, accepted_at, expires_at);
`)
	return err
}

func (s *workspaceService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workspaces", s.listWorkspaces)
	mux.HandleFunc("POST /api/v1/workspaces/switch", s.switchWorkspace)
	mux.HandleFunc("GET /api/v1/workspace/members", s.listMembers)
	mux.HandleFunc("POST /api/v1/workspace/invites", s.createInvite)
	mux.HandleFunc("POST /api/v1/workspace/invites/accept", s.acceptInvite)
	mux.HandleFunc("PUT /api/v1/workspace/members/{user_id}", s.updateMember)
	mux.HandleFunc("DELETE /api/v1/workspace/members/{user_id}", s.removeMember)
}

type workspaceMemberView struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

func (s *workspaceService) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	userID, selectedWorkspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	rows, err := s.auth.db.QueryContext(r.Context(), `
SELECT w.id, w.name, m.role, (w.id = ?)
FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
WHERE m.user_id = ?
ORDER BY w.id`, selectedWorkspaceID, userID)
	if err != nil {
		log.Printf("list workspaces: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load workspaces")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, role string
		var selected bool
		if err := rows.Scan(&id, &name, &role, &selected); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load workspaces")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "role": role, "selected": selected})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load workspaces")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": items})
}

func (s *workspaceService) switchWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, _, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	var input struct {
		WorkspaceID int64 `json:"workspace_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil || input.WorkspaceID < 1 {
		writeError(w, http.StatusBadRequest, "A valid workspace_id is required")
		return
	}
	var exists int
	err = s.auth.db.QueryRowContext(r.Context(), `SELECT 1 FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, input.WorkspaceID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusForbidden, "You are not a member of that workspace")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not switch workspace")
		return
	}
	result, err := s.auth.db.ExecContext(r.Context(), `UPDATE sessions SET workspace_id = ? WHERE token_hash = ? AND user_id = ?`, input.WorkspaceID, sessionTokenHash(r), userID)
	if err != nil {
		log.Printf("switch workspace: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not switch workspace")
		return
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	var workspaceName, role string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT w.name, m.role FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id WHERE w.id = ? AND m.user_id = ?`, input.WorkspaceID, userID).Scan(&workspaceName, &role); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load workspace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace_id": input.WorkspaceID, "workspace": workspaceName, "workspace_role": role})
}

func (s *workspaceService) listMembers(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	if _, err := s.requireWorkspaceRole(r.Context(), userID, workspaceID, "viewer", "developer", "owner"); err != nil {
		writeError(w, http.StatusForbidden, "Workspace membership required")
		return
	}
	rows, err := s.auth.db.QueryContext(r.Context(), `SELECT u.id, u.name, u.email, m.role FROM workspace_members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = ? ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, u.name, u.id`, workspaceID)
	if err != nil {
		log.Printf("list workspace members: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load workspace members")
		return
	}
	defer rows.Close()
	members := make([]workspaceMemberView, 0)
	for rows.Next() {
		var member workspaceMemberView
		if err := rows.Scan(&member.UserID, &member.Name, &member.Email, &member.Role); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load workspace members")
			return
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load workspace members")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (s *workspaceService) createInvite(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	if _, err := s.requireWorkspaceRole(r.Context(), userID, workspaceID, "owner"); err != nil {
		writeError(w, http.StatusForbidden, "Only a workspace owner can invite members")
		return
	}
	var input struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter an email and role")
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(input.Email) {
		writeError(w, http.StatusBadRequest, "Enter a valid email address")
		return
	}
	if !validWorkspaceRole(input.Role) || input.Role == "owner" {
		writeError(w, http.StatusBadRequest, "Role must be developer or viewer")
		return
	}
	var pendingInvites int
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM workspace_invites WHERE workspace_id = ? AND accepted_at IS NULL AND expires_at > ?`, workspaceID, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&pendingInvites); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create invitation")
		return
	}
	if pendingInvites >= 10 {
		writeError(w, http.StatusTooManyRequests, "This workspace already has ten pending invitations")
		return
	}
	var existing int
	err = s.auth.db.QueryRowContext(r.Context(), `SELECT 1 FROM users u JOIN workspace_members m ON m.user_id = u.id WHERE m.workspace_id = ? AND u.email = ?`, workspaceID, input.Email).Scan(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "That user is already a workspace member")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Could not create invitation")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create invitation")
		return
	}
	now := time.Now().UTC()
	_, err = s.auth.db.ExecContext(r.Context(), `INSERT INTO workspace_invites (token_hash, workspace_id, email, role, invited_by, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, tokenDigest(token), workspaceID, input.Email, input.Role, userID, now.Add(workspaceInviteLifetime).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		log.Printf("create workspace invite: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create invitation")
		return
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, user_id, action, details, created_at) VALUES (?, ?, 'workspace.invite', ?, ?)`, workspaceID, userID, input.Email+" ("+input.Role+")", now.Format(time.RFC3339Nano))
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "email": input.Email, "role": input.Role, "expires_at": now.Add(workspaceInviteLifetime).Format(time.RFC3339Nano)})
}

func (s *workspaceService) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, _, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &input); err != nil || strings.TrimSpace(input.Token) == "" {
		writeError(w, http.StatusBadRequest, "An invitation token is required")
		return
	}
	var userEmail string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id = ?`, userID).Scan(&userEmail); err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	tx, err := s.auth.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not accept invitation")
		return
	}
	defer tx.Rollback()
	var workspaceID int64
	var role, inviteEmail, expiry string
	err = tx.QueryRowContext(r.Context(), `SELECT workspace_id, role, email, expires_at FROM workspace_invites WHERE token_hash = ? AND accepted_at IS NULL`, tokenDigest(strings.TrimSpace(input.Token))).Scan(&workspaceID, &role, &inviteEmail, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Invitation is invalid or already used")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not accept invitation")
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !deadline.After(time.Now()) {
		writeError(w, http.StatusGone, "Invitation has expired")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(inviteEmail), strings.TrimSpace(userEmail)) {
		writeError(w, http.StatusForbidden, "Sign in with the email address this invitation was sent to")
		return
	}
	if !validWorkspaceRole(role) || role == "owner" {
		writeError(w, http.StatusConflict, "Invitation has an invalid role")
		return
	}
	var existing int
	err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID).Scan(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "You are already a member of this workspace")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Could not accept invitation")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES (?, ?, ?)`, workspaceID, userID, role); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not accept invitation")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE workspace_invites SET accepted_at = ? WHERE token_hash = ? AND accepted_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), tokenDigest(strings.TrimSpace(input.Token))); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not accept invitation")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not accept invitation")
		return
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `UPDATE sessions SET workspace_id = ? WHERE token_hash = ?`, workspaceID, sessionTokenHash(r))
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, user_id, action, details, created_at) VALUES (?, ?, 'workspace.member.accept', ?, ?)`, workspaceID, userID, role, time.Now().UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "role": role})
}

func (s *workspaceService) updateMember(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	actorID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	if _, err := s.requireWorkspaceRole(r.Context(), actorID, workspaceID, "owner"); err != nil {
		writeError(w, http.StatusForbidden, "Only a workspace owner can change member roles")
		return
	}
	targetID, err := strconv.ParseInt(r.PathValue("user_id"), 10, 64)
	if err != nil || targetID < 1 {
		writeError(w, http.StatusBadRequest, "Invalid member ID")
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &input); err != nil || !validWorkspaceRole(input.Role) || input.Role == "owner" {
		writeError(w, http.StatusBadRequest, "Role must be developer or viewer")
		return
	}
	var currentRole string
	err = s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, targetID).Scan(&currentRole)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Workspace member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update member")
		return
	}
	if currentRole == "owner" {
		writeError(w, http.StatusConflict, "Workspace owners cannot be demoted")
		return
	}
	if _, err := s.auth.db.ExecContext(r.Context(), `UPDATE workspace_members SET role = ? WHERE workspace_id = ? AND user_id = ?`, input.Role, workspaceID, targetID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update member")
		return
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, user_id, action, details, created_at) VALUES (?, ?, 'workspace.member.role', ?, ?)`, workspaceID, actorID, fmt.Sprintf("user %d: %s", targetID, input.Role), time.Now().UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, map[string]any{"user_id": targetID, "role": input.Role})
}

func (s *workspaceService) removeMember(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	actorID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	if _, err := s.requireWorkspaceRole(r.Context(), actorID, workspaceID, "owner"); err != nil {
		writeError(w, http.StatusForbidden, "Only a workspace owner can remove members")
		return
	}
	targetID, err := strconv.ParseInt(r.PathValue("user_id"), 10, 64)
	if err != nil || targetID < 1 {
		writeError(w, http.StatusBadRequest, "Invalid member ID")
		return
	}
	var role string
	err = s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, targetID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Workspace member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not remove member")
		return
	}
	if role == "owner" {
		writeError(w, http.StatusConflict, "Workspace owners cannot be removed")
		return
	}
	if _, err := s.auth.db.ExecContext(r.Context(), `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, targetID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not remove member")
		return
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, user_id, action, details, created_at) VALUES (?, ?, 'workspace.member.remove', ?, ?)`, workspaceID, actorID, fmt.Sprintf("user %d", targetID), time.Now().UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *workspaceService) requireWorkspaceRole(ctx context.Context, userID, workspaceID int64, allowed ...string) (string, error) {
	var role string
	if err := s.auth.db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&role); err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		if role == candidate {
			return role, nil
		}
	}
	return role, errors.New("workspace role is not allowed")
}

func validWorkspaceRole(role string) bool {
	return role == "owner" || role == "developer" || role == "viewer"
}

func sessionTokenHash(r *http.Request) string {
	cookie, _ := r.Cookie(sessionCookieName)
	if cookie == nil {
		return ""
	}
	return hashToken(cookie.Value)
}

func tokenDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (a *authService) userWithSelectedWorkspace(ctx context.Context, userID, workspaceID int64) (authUser, error) {
	var user authUser
	err := a.db.QueryRowContext(ctx, `
SELECT u.id, u.name, u.email, w.id, w.name, m.role
FROM users u JOIN workspace_members m ON m.user_id = u.id
JOIN workspaces w ON w.id = m.workspace_id
WHERE u.id = ? AND w.id = ?`, userID, workspaceID).Scan(&user.ID, &user.Name, &user.Email, &user.WorkspaceID, &user.Workspace, &user.Role)
	return user, err
}

// sessionWorkspace returns the selected workspace for this authenticated
// session, falling back to the user's first workspace for pre-migration rows.
func (a *authService) sessionWorkspace(r *http.Request) (int64, int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, 0, errors.New("session cookie missing")
	}
	var userID int64
	var expiresAt string
	var selected sql.NullInt64
	err = a.db.QueryRowContext(r.Context(), `SELECT user_id, expires_at, workspace_id FROM sessions WHERE token_hash = ?`, hashToken(cookie.Value)).Scan(&userID, &expiresAt, &selected)
	if err != nil {
		return 0, 0, err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !expires.After(time.Now()) {
		return 0, 0, errors.New("session expired")
	}
	if selected.Valid {
		var membership int
		err := a.db.QueryRowContext(r.Context(), `SELECT 1 FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, selected.Int64).Scan(&membership)
		if err == nil {
			return userID, selected.Int64, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, err
		}
	}
	var workspaceID int64
	err = a.db.QueryRowContext(r.Context(), `SELECT workspace_id FROM workspace_members WHERE user_id = ? ORDER BY CASE role WHEN 'owner' THEN 0 ELSE 1 END, workspace_id LIMIT 1`, userID).Scan(&workspaceID)
	return userID, workspaceID, err
}
