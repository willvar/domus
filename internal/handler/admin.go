package handler

import (
	"net/http"
	"strings"
	"time"

	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
)

func validUsername(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] == '.' || value[0] == '-' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := h.Repos.Users.List()
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "list_users_failed"})
	}
	if users == nil {
		users = []model.User{}
	}
	return httpx.JSON(w, http.StatusOK, users)
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}

	if body.Username == "" || body.Password == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "username_password_required"})
	}
	if !validUsername(body.Username) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_username"})
	}
	if body.Role == "" {
		body.Role = "user"
	}
	if !isValidUserRole(body.Role) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_role"})
	}

	wrappedKEKHex, err := h.generateWrappedKEK()
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "key_generation_failed"})
	}

	user, err := h.Repos.Users.Create(body.Username, body.Password, body.Role, wrappedKEKHex)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return httpx.JSON(w, 409, map[string]any{"error": "username_exists"})
		}
		return httpx.JSON(w, 500, map[string]any{"error": "create_user_failed"})
	}

	if h.DOFS == nil {
		return httpx.JSON(w, 500, map[string]any{"error": "dofs_unavailable"})
	}
	if err := h.DOFS.EnsureUser(r.Context(), user); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "dofs_namespace_failed"})
	}

	h.audit(r, "user_create", user.Username, body.Role, "success", 0)
	return httpx.JSON(w, 201, user)
}

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if id == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_user_id"})
	}

	var body struct {
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}

	user, err := h.Repos.Users.GetByID(id)
	if err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "user_not_found"})
	}

	if body.Password != "" {
		if err := h.Repos.Users.UpdatePassword(user.ID, body.Password); err != nil {
			return httpx.JSON(w, 500, map[string]any{"error": "update_password_failed"})
		}
	}

	role := user.Role
	if body.Role != "" {
		if !isValidUserRole(body.Role) {
			return httpx.JSON(w, 400, map[string]any{"error": "invalid_role"})
		}
		if code, guardErr := ensureNotDemotingLastRoot(h.Repos.Users, user, body.Role); guardErr != nil {
			return httpx.JSON(w, 500, map[string]any{"error": "update_user_failed"})
		} else if code != "" {
			return httpx.JSON(w, 400, map[string]any{"error": code})
		}
		role = body.Role
	}

	if err := h.Repos.Users.UpdateRole(user.ID, role); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "update_user_failed"})
	}

	// If role changed, invalidate sessions
	if body.Role != "" && body.Role != user.Role {
		h.revokeUserSessions(user.ID)
	}

	h.audit(r, "user_update", user.Username, "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if id == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_user_id"})
	}

	session := middleware.Session(r)
	if id == session.UserID {
		return httpx.JSON(w, 400, map[string]any{"error": "cannot_delete_self"})
	}

	user, err := h.Repos.Users.GetByID(id)
	if err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "user_not_found"})
	}
	if code, guardErr := ensureNotDeletingLastRoot(h.Repos.Users, user); guardErr != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "delete_user_failed"})
	} else if code != "" {
		return httpx.JSON(w, 400, map[string]any{"error": code})
	}

	if err := h.deleteUserCompletely(user); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "delete_user_failed"})
	}

	h.audit(r, "user_delete", user.Username, "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleResetUserOTP(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := h.Repos.Users.GetByID(id); err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "user_not_found"})
	}
	if err := h.Repos.Users.UpdateTOTP(id, "", false); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "update_failed"})
	}
	h.revokeUserSessions(id)
	h.audit(r, "user_reset_otp", id, "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleResetUserEmail(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := h.Repos.Users.GetByID(id); err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "user_not_found"})
	}
	if err := h.Repos.Users.UpdateEmail(id, ""); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "update_failed"})
	}
	h.revokeUserSessions(id)
	h.audit(r, "user_reset_email", id, "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Admin audit log query ---

func (h *Handler) handleListAuditLogs(w http.ResponseWriter, r *http.Request) error {
	page := httpx.QueryInt(r, "page", 1)
	size := httpx.QueryInt(r, "size", 50)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}

	filter := model.AuditFilter{
		User:   httpx.Query(r, "user"),
		Action: httpx.Query(r, "action"),
		Page:   page,
		Size:   size,
	}
	if from := httpx.Query(r, "from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			filter.From = &t
		}
	}
	if to := httpx.Query(r, "to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			filter.To = &t
		}
	}

	logs, total, err := h.Repos.Audit.ListLogs(filter)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "list_audit_logs_failed"})
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{
		"total": total,
		"page":  page,
		"size":  size,
		"items": logs,
	})
}

// --- Preview duration (from sendBeacon) ---

func (h *Handler) handleAuditPreview(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Path       string `json:"path"`
		DurationMs int64  `json:"duration_ms"`
		Type       string `json:"type"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || body.Path == "" {
		return httpx.Status(w, 204)
	}
	h.audit(r, "file_preview", body.Path, body.Type, "success", body.DurationMs)
	return httpx.Status(w, 204)
}

// handleCORSCheck returns presigned PUT and DELETE URLs for a temporary probe object.
// The frontend uses these to verify that the OSS bucket's CORS is correctly configured
// for browser-direct uploads.
func (h *Handler) handleCORSCheck(w http.ResponseWriter, r *http.Request) error {
	probeKey := ".cors-probe"
	putURL, err := h.Store.PresignedPutObject(probeKey, 5*time.Minute)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "presign_put_failed"})
	}
	deleteURL, err := h.Store.PresignedDeleteObject(probeKey, 5*time.Minute)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "presign_delete_failed"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"put_url":    putURL,
		"delete_url": deleteURL,
	})
}
