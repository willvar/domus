package handler

import (
	"errors"
	"log"
	"net/http"
	"net/mail"

	"github.com/willvar/dofs"

	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
)

func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email
}

func (h *Handler) handleStorageUsage(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	if h.DOFS == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	size, count, err := h.DOFS.Usage(r.Context(), session.UserID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{
		"size":  size,
		"count": count,
	})
}

func (h *Handler) handleUpdateHistoryLimit(w http.ResponseWriter, r *http.Request) error {
	if h.DOFS == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	var body struct {
		HistoryLimit int64 `json:"history_limit"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || body.HistoryLimit < dofs.DefaultHistoryLimit {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_history_limit"})
	}
	session := middleware.Session(r)
	namespace, err := h.DOFS.SetHistoryLimit(r.Context(), session.UserID, body.HistoryLimit)
	if errors.Is(err, dofs.ErrInvalidNamespace) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_history_limit"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_limit_update_failed"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"history_limit": namespace.HistoryLimit,
	})
}

// handleSecurityStatus returns the user's current security configuration.
func (h *Handler) handleSecurityStatus(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{
		"email":        user.Email,
		"has_email":    user.Email != "",
		"totp_enabled": user.TOTPEnabled,
		"smtp_enabled": h.Email.Configured(),
	})
}

func (h *Handler) handleUpdateDisplayName(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}

	session := middleware.Session(r)
	if err := h.Repos.Users.UpdateDisplayName(session.UserID, body.DisplayName); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "update_failed"})
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleBindEmail sends a verification code to the provided email address.
func (h *Handler) handleBindEmail(w http.ResponseWriter, r *http.Request) error {
	if !h.Email.Configured() {
		return httpx.JSON(w, 400, map[string]any{"error": "email_not_configured"})
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || !isValidEmail(body.Email) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_email"})
	}

	session := middleware.Session(r)
	code := service.GenerateEmailCode()
	h.Challenges.StoreEmailBindCode(session.UserID, body.Email, code)

	go func() {
		if err := h.Email.SendVerification(body.Email, code); err != nil {
			log.Printf("[account] failed to send bind email to %s: %v", body.Email, err)
		}
	}()

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleVerifyBindEmail confirms email binding with a verification code.
func (h *Handler) handleVerifyBindEmail(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}

	session := middleware.Session(r)
	if !h.Challenges.VerifyEmailBindCode(session.UserID, body.Email, body.Code) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_code"})
	}

	if err := h.Repos.Users.UpdateEmail(session.UserID, body.Email); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "update_email_failed"})
	}

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUnbindEmail removes the user's email binding.
func (h *Handler) handleUnbindEmail(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	if err := h.Repos.Users.UpdateEmail(session.UserID, ""); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "unbind_email_failed"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOTPSetup generates a new TOTP secret and returns it with the QR URI.
func (h *Handler) handleOTPSetup(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}

	if user.TOTPEnabled {
		return httpx.JSON(w, 400, map[string]any{"error": "otp_already_enabled"})
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}

	h.Challenges.StorePendingTOTP(session.UserID, secret)

	return httpx.JSON(w, http.StatusOK, map[string]any{
		"secret": secret,
		"uri":    auth.GenerateTOTPURI(secret, user.Username),
	})
}

// handleOTPEnable confirms OTP setup by verifying a code from the authenticator app.
func (h *Handler) handleOTPEnable(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}

	session := middleware.Session(r)
	secret := h.Challenges.GetPendingTOTP(session.UserID)
	if secret == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "no_pending_otp"})
	}

	if !auth.ValidateTOTP(secret, body.Code) {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_code"})
	}

	if err := h.Repos.Users.UpdateTOTP(session.UserID, secret, true); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "enable_otp_failed"})
	}
	h.Challenges.DeletePendingTOTP(session.UserID)

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOTPDisable disables OTP for the current user.
func (h *Handler) handleOTPDisable(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	if err := h.Repos.Users.UpdateTOTP(session.UserID, "", false); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "disable_otp_failed"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChangePassword changes the user's password and invalidates other sessions.
func (h *Handler) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	if body.NewPassword == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "new_password_required"})
	}

	session := middleware.Session(r)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}

	if !model.CheckPassword(user.PasswordHash, body.OldPassword) {
		// Use 400 instead of 401 to avoid triggering the frontend auth:expired interceptor
		return httpx.JSON(w, 400, map[string]any{"error": "wrong_password"})
	}

	if err := h.Repos.Users.UpdatePassword(user.ID, body.NewPassword); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "update_password_failed"})
	}

	// Invalidate all other sessions, keep current one
	currentSessionID := middleware.SessionID(r)
	h.Repos.Sessions.DeleteByUserIDExcept(user.ID, currentSessionID)

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
