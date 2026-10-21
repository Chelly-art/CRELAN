package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type workspaceAccessGrant struct {
	ID           int64  `json:"id"`
	WorkspaceID  int64  `json:"workspace_id"`
	UserID       int64  `json:"user_id"`
	UserName     string `json:"user_name"`
	UserEmail    string `json:"user_email"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Permission   string `json:"permission"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type accessGrantInput struct {
	UserID       int64  `json:"user_id"`
	UserEmail    string `json:"user_email,omitempty"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Permission   string `json:"permission"`
}

type accessCheckInput struct {
	UserID             int64  `json:"user_id"`
	ResourceType       string `json:"resource_type"`
	ResourceID         string `json:"resource_id"`
	RequiredPermission string `json:"required_permission"`
}

type accessCheckResult struct {
	Allowed           bool   `json:"allowed"`
	Reason            string `json:"reason"`
	GrantedPermission string `json:"granted_permission,omitempty"`
}

// CheckUserAccess verifies whether a given user possesses the required permission level
// on a target resource within their workspace.
func CheckUserAccess(ctx context.Context, db *sql.DB, userID, workspaceID int64, resourceType, resourceID, requiredPerm string) (bool, string, error) {
	if db == nil {
		return true, "Database not configured; permissive mode", nil
	}

	requiredPerm = strings.ToLower(strings.TrimSpace(requiredPerm))
	if requiredPerm == "" {
		requiredPerm = "read"
	}

	// 1. Verify workspace membership and base role
	var wsRole string
	err := db.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&wsRole)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "User is not a member of this workspace", nil
	} else if err != nil {
		return false, "", fmt.Errorf("query workspace membership: %w", err)
	}

	// Workspace owner has unconditional administrative access
	if wsRole == "owner" {
		return true, "Workspace owner possesses full administrative privileges", nil
	}

	// 2. Query explicit granular grants for this user & resource
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if resourceType == "" {
		resourceType = "cluster"
	}
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		resourceID = "*"
	}

	var explicitPerm string
	err = db.QueryRowContext(ctx, `
		SELECT permission FROM workspace_access_grants 
		WHERE workspace_id = ? AND user_id = ? AND resource_type = ? AND (resource_id = ? OR resource_id = '*')
		ORDER BY CASE permission WHEN 'admin' THEN 1 WHEN 'write' THEN 2 ELSE 3 END LIMIT 1`,
		workspaceID, userID, resourceType, resourceID).Scan(&explicitPerm)

	if err == nil && explicitPerm != "" {
		if permissionSatisfies(explicitPerm, requiredPerm) {
			return true, fmt.Sprintf("Explicit grant '%s' on %s (%s) satisfies '%s'", explicitPerm, resourceType, resourceID, requiredPerm), nil
		}
		return false, fmt.Sprintf("Explicit grant '%s' on %s (%s) is insufficient for '%s'", explicitPerm, resourceType, resourceID, requiredPerm), nil
	}

	// 3. Fallback to baseline workspace role
	switch wsRole {
	case "developer":
		if requiredPerm == "read" || requiredPerm == "write" {
			return true, fmt.Sprintf("Workspace role 'developer' permits %s", requiredPerm), nil
		}
		return false, fmt.Sprintf("Workspace role 'developer' does not permit '%s' without explicit admin grant", requiredPerm), nil
	case "viewer":
		if requiredPerm == "read" {
			return true, "Workspace role 'viewer' permits read-only access", nil
		}
		return false, fmt.Sprintf("Workspace role 'viewer' is read-only; '%s' required", requiredPerm), nil
	default:
		return false, fmt.Sprintf("Unknown workspace role '%s'", wsRole), nil
	}
}

func permissionSatisfies(granted, required string) bool {
	granted = strings.ToLower(granted)
	required = strings.ToLower(required)
	if granted == "admin" {
		return true
	}
	if granted == "write" {
		return required == "read" || required == "write"
	}
	if granted == "read" {
		return required == "read"
	}
	return false
}

// -------------------------------------------------------------
// HTTP Handlers for Granular Access Control
// -------------------------------------------------------------

func (s *adminService) listAccessGrants(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}

	rows, err := s.auth.db.QueryContext(r.Context(), `
		SELECT g.id, g.workspace_id, g.user_id, COALESCE(u.name, ''), COALESCE(u.email, ''), g.resource_type, g.resource_id, g.permission, g.created_at, g.updated_at
		FROM workspace_access_grants g
		JOIN users u ON u.id = g.user_id
		WHERE g.workspace_id = ?
		ORDER BY g.resource_type, g.resource_id, u.name`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load access grants: "+err.Error())
		return
	}
	defer rows.Close()

	var grants []workspaceAccessGrant
	for rows.Next() {
		var g workspaceAccessGrant
		if err := rows.Scan(&g.ID, &g.WorkspaceID, &g.UserID, &g.UserName, &g.UserEmail, &g.ResourceType, &g.ResourceID, &g.Permission, &g.CreatedAt, &g.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not scan access grant: "+err.Error())
			return
		}
		grants = append(grants, g)
	}
	if grants == nil {
		grants = []workspaceAccessGrant{}
	}

	// Also fetch workspace members to populate dropdowns
	memberRows, err := s.auth.db.QueryContext(r.Context(), `
		SELECT u.id, u.name, u.email, m.role
		FROM workspace_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ?
		ORDER BY u.name`, workspaceID)
	var members []workspaceMemberView
	if err == nil {
		defer memberRows.Close()
		for memberRows.Next() {
			var m workspaceMemberView
			if err := memberRows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role); err == nil {
				members = append(members, m)
			}
		}
	}
	if members == nil {
		members = []workspaceMemberView{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"grants":  grants,
		"members": members,
	})
}

func (s *adminService) createOrUpdateAccessGrant(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}

	// Verify operator is workspace owner or admin
	var actorRole string
	_ = s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&actorRole)
	if actorRole != "owner" {
		writeError(w, http.StatusForbidden, "Only workspace owners can manage granular access grants")
		return
	}

	var input accessGrantInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// Resolve user ID if email is provided
	if input.UserID == 0 && input.UserEmail != "" {
		_ = s.auth.db.QueryRowContext(r.Context(), `SELECT id FROM users WHERE email = ? COLLATE NOCASE`, strings.TrimSpace(input.UserEmail)).Scan(&input.UserID)
	}
	if input.UserID == 0 {
		writeError(w, http.StatusBadRequest, "Valid user ID or email is required")
		return
	}

	input.ResourceType = strings.ToLower(strings.TrimSpace(input.ResourceType))
	if input.ResourceType == "" {
		input.ResourceType = "cluster"
	}
	validTypes := map[string]bool{"cluster": true, "deployment": true, "database": true, "service": true, "storage": true, "settings": true}
	if !validTypes[input.ResourceType] {
		writeError(w, http.StatusBadRequest, "Resource type must be cluster, deployment, database, service, storage, or settings")
		return
	}

	input.ResourceID = strings.TrimSpace(input.ResourceID)
	if input.ResourceID == "" {
		input.ResourceID = "*"
	}

	input.Permission = strings.ToLower(strings.TrimSpace(input.Permission))
	if input.Permission != "read" && input.Permission != "write" && input.Permission != "admin" {
		writeError(w, http.StatusBadRequest, "Permission must be read, write, or admin")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.auth.db.ExecContext(r.Context(), `
		INSERT INTO workspace_access_grants (workspace_id, user_id, resource_type, resource_id, permission, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, user_id, resource_type, resource_id)
		DO UPDATE SET permission = excluded.permission, updated_at = excluded.updated_at`,
		workspaceID, input.UserID, input.ResourceType, input.ResourceID, input.Permission, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save access grant: "+err.Error())
		return
	}

	grantID, _ := res.LastInsertId()
	_ = recordPlatformAudit(s.auth.db, "admin", "access_grant.create", "AccessGrant", "platform", fmt.Sprintf("user-%d", input.UserID), fmt.Sprintf("Granted %s on %s/%s to user %d", input.Permission, input.ResourceType, input.ResourceID, input.UserID))

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "saved",
		"id":            grantID,
		"resource_type": input.ResourceType,
		"resource_id":   input.ResourceID,
		"permission":    input.Permission,
	})
}

func (s *adminService) deleteAccessGrant(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}

	var actorRole string
	_ = s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&actorRole)
	if actorRole != "owner" {
		writeError(w, http.StatusForbidden, "Only workspace owners can revoke access grants")
		return
	}

	grantIDStr := r.PathValue("id")
	grantID, err := strconv.ParseInt(grantIDStr, 10, 64)
	if err != nil || grantID <= 0 {
		writeError(w, http.StatusBadRequest, "Invalid grant ID")
		return
	}

	_, err = s.auth.db.ExecContext(r.Context(), `DELETE FROM workspace_access_grants WHERE id = ? AND workspace_id = ?`, grantID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete access grant: "+err.Error())
		return
	}

	_ = recordPlatformAudit(s.auth.db, "admin", "access_grant.delete", "AccessGrant", "platform", fmt.Sprintf("grant-%d", grantID), "Revoked access grant")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *adminService) checkAccessEndpoint(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}

	var input accessCheckInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	allowed, reason, err := CheckUserAccess(r.Context(), s.auth.db, input.UserID, workspaceID, input.ResourceType, input.ResourceID, input.RequiredPermission)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Check failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, accessCheckResult{
		Allowed: allowed,
		Reason:  reason,
	})
}
