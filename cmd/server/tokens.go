package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var tokenScopes = map[string]struct{}{
	"deployments:read":  {},
	"deployments:write": {},
	"resources:read":    {},
	"logs:read":         {},
	"usage:read":        {},
	"audit:read":        {},
}

type workspaceToken struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func (a *authService) listTokens(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, err := a.sessionIdentity(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT id, name, token_prefix, scopes, created_at, expires_at, revoked_at
FROM api_tokens
WHERE workspace_id = ?
ORDER BY created_at DESC, id DESC`, workspaceID)
	if err != nil {
		log.Printf("list workspace tokens: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load access tokens")
		return
	}
	defer rows.Close()

	tokens := make([]workspaceToken, 0)
	for rows.Next() {
		var token workspaceToken
		var scopesJSON, createdAt string
		var expiresAt, revokedAt sql.NullString
		if err := rows.Scan(&token.ID, &token.Name, &token.Prefix, &scopesJSON, &createdAt, &expiresAt, &revokedAt); err != nil {
			log.Printf("read workspace token: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load access tokens")
			return
		}
		if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
			log.Printf("decode workspace token scopes: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load access tokens")
			return
		}
		token.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			log.Printf("parse workspace token creation date: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not load access tokens")
			return
		}
		if expiresAt.Valid {
			expires, parseErr := time.Parse(time.RFC3339Nano, expiresAt.String)
			if parseErr != nil {
				log.Printf("parse workspace token expiry: %v", parseErr)
				writeError(w, http.StatusInternalServerError, "Could not load access tokens")
				return
			}
			token.ExpiresAt = &expires
		}
		if revokedAt.Valid {
			revoked, parseErr := time.Parse(time.RFC3339Nano, revokedAt.String)
			if parseErr != nil {
				log.Printf("parse workspace token revocation: %v", parseErr)
				writeError(w, http.StatusInternalServerError, "Could not load access tokens")
				return
			}
			token.RevokedAt = &revoked
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate workspace tokens: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load access tokens")
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (a *authService) createToken(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	_, workspaceID, err := a.sessionIdentity(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	var input struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays *int     `json:"expires_in_days"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter a token name, scopes, and optional expiry")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) == 0 || len(input.Name) > 80 {
		writeError(w, http.StatusBadRequest, "Token name must be between 1 and 80 characters")
		return
	}
	if len(input.Scopes) == 0 || len(input.Scopes) > len(tokenScopes) {
		writeError(w, http.StatusBadRequest, "Select at least one valid access scope")
		return
	}
	scopes := make(map[string]struct{}, len(input.Scopes))
	for _, scope := range input.Scopes {
		if _, valid := tokenScopes[scope]; !valid {
			writeError(w, http.StatusBadRequest, "Unknown access scope: "+scope)
			return
		}
		scopes[scope] = struct{}{}
	}
	if len(scopes) != len(input.Scopes) {
		writeError(w, http.StatusBadRequest, "Duplicate access scopes are not allowed")
		return
	}
	if input.ExpiresInDays != nil && (*input.ExpiresInDays < 1 || *input.ExpiresInDays > 365) {
		writeError(w, http.StatusBadRequest, "Token expiry must be between 1 and 365 days")
		return
	}

	scopeList := make([]string, 0, len(scopes))
	for scope := range scopes {
		scopeList = append(scopeList, scope)
	}
	sort.Strings(scopeList)
	scopeJSON, err := json.Marshal(scopeList)
	if err != nil {
		log.Printf("encode token scopes: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create access token")
		return
	}
	secret, err := randomToken(32)
	if err != nil {
		log.Printf("generate API token: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create access token")
		return
	}
	secret = "crl_" + secret
	now := time.Now().UTC()
	var expiresAt any
	var expiry *time.Time
	if input.ExpiresInDays != nil {
		value := now.AddDate(0, 0, *input.ExpiresInDays)
		expiry = &value
		expiresAt = value.Format(time.RFC3339Nano)
	}
	result, err := a.db.ExecContext(r.Context(), `
INSERT INTO api_tokens (workspace_id, name, token_hash, token_prefix, scopes, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		workspaceID, input.Name, hashToken(secret), secret[:12], string(scopeJSON), expiresAt, now.Format(time.RFC3339Nano))
	if err != nil {
		log.Printf("persist API token: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create access token")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		log.Printf("read created API token ID: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not create access token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": input.Name, "token": secret, "prefix": secret[:12],
		"scopes": scopeList, "created_at": now, "expires_at": expiry,
	})
}

func (a *authService) revokeToken(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	_, workspaceID, err := a.sessionIdentity(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "Invalid token ID")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := a.db.ExecContext(r.Context(), `
UPDATE api_tokens SET revoked_at = ?
WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL`, now, id, workspaceID)
	if err != nil {
		log.Printf("revoke API token: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not revoke access token")
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		log.Printf("read revoked token count: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not revoke access token")
		return
	}
	if affected == 0 {
		var exists bool
		err := a.db.QueryRowContext(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM api_tokens WHERE id = ? AND workspace_id = ?)`,
			id, workspaceID).Scan(&exists)
		if err != nil {
			log.Printf("check token for revocation: %v", err)
			writeError(w, http.StatusInternalServerError, "Could not revoke access token")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "Access token not found")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *authService) sessionIdentity(r *http.Request) (userID, workspaceID int64, err error) {
	return a.sessionWorkspace(r)
}
