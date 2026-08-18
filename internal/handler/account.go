package handler

import (
	"log"
	"net/mail"

	"github.com/gofiber/fiber/v2"

	"domus/internal/auth"
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

func (h *Handler) handleStorageUsage(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	if h.DOFS == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
	}
	size, count, err := h.DOFS.Usage(c.UserContext(), session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	return c.JSON(fiber.Map{
		"size":  size,
		"count": count,
	})
}

// handleSecurityStatus returns the user's current security configuration.
func (h *Handler) handleSecurityStatus(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	return c.JSON(fiber.Map{
		"email":        user.Email,
		"has_email":    user.Email != "",
		"totp_enabled": user.TOTPEnabled,
		"smtp_enabled": h.Email.Configured(),
	})
}

func (h *Handler) handleUpdateDisplayName(c *fiber.Ctx) error {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	if err := h.Repos.Users.UpdateDisplayName(session.UserID, body.DisplayName); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_failed"})
	}

	return c.JSON(fiber.Map{"ok": true})
}

// handleBindEmail sends a verification code to the provided email address.
func (h *Handler) handleBindEmail(c *fiber.Ctx) error {
	if !h.Email.Configured() {
		return c.Status(400).JSON(fiber.Map{"error": "email_not_configured"})
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&body); err != nil || !isValidEmail(body.Email) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_email"})
	}

	session := c.Locals("session").(*model.Session)
	code := service.GenerateEmailCode()
	h.Challenges.StoreEmailBindCode(session.UserID, body.Email, code)

	go func() {
		if err := h.Email.SendVerification(body.Email, code); err != nil {
			log.Printf("[account] failed to send bind email to %s: %v", body.Email, err)
		}
	}()

	return c.JSON(fiber.Map{"ok": true})
}

// handleVerifyBindEmail confirms email binding with a verification code.
func (h *Handler) handleVerifyBindEmail(c *fiber.Ctx) error {
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	if !h.Challenges.VerifyEmailBindCode(session.UserID, body.Email, body.Code) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_code"})
	}

	if err := h.Repos.Users.UpdateEmail(session.UserID, body.Email); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_email_failed"})
	}

	return c.JSON(fiber.Map{"ok": true})
}

// handleUnbindEmail removes the user's email binding.
func (h *Handler) handleUnbindEmail(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	if err := h.Repos.Users.UpdateEmail(session.UserID, ""); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "unbind_email_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// handleOTPSetup generates a new TOTP secret and returns it with the QR URI.
func (h *Handler) handleOTPSetup(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	if user.TOTPEnabled {
		return c.Status(400).JSON(fiber.Map{"error": "otp_already_enabled"})
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	h.Challenges.StorePendingTOTP(session.UserID, secret)

	return c.JSON(fiber.Map{
		"secret": secret,
		"uri":    auth.GenerateTOTPURI(secret, user.Username),
	})
}

// handleOTPEnable confirms OTP setup by verifying a code from the authenticator app.
func (h *Handler) handleOTPEnable(c *fiber.Ctx) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)
	secret := h.Challenges.GetPendingTOTP(session.UserID)
	if secret == "" {
		return c.Status(400).JSON(fiber.Map{"error": "no_pending_otp"})
	}

	if !auth.ValidateTOTP(secret, body.Code) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_code"})
	}

	if err := h.Repos.Users.UpdateTOTP(session.UserID, secret, true); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "enable_otp_failed"})
	}
	h.Challenges.DeletePendingTOTP(session.UserID)

	return c.JSON(fiber.Map{"ok": true})
}

// handleOTPDisable disables OTP for the current user.
func (h *Handler) handleOTPDisable(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	if err := h.Repos.Users.UpdateTOTP(session.UserID, "", false); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "disable_otp_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// handleChangePassword changes the user's password and invalidates other sessions.
func (h *Handler) handleChangePassword(c *fiber.Ctx) error {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.NewPassword == "" {
		return c.Status(400).JSON(fiber.Map{"error": "new_password_required"})
	}

	session := c.Locals("session").(*model.Session)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	if !model.CheckPassword(user.PasswordHash, body.OldPassword) {
		// Use 400 instead of 401 to avoid triggering the frontend auth:expired interceptor
		return c.Status(400).JSON(fiber.Map{"error": "wrong_password"})
	}

	if err := h.Repos.Users.UpdatePassword(user.ID, body.NewPassword); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "update_password_failed"})
	}

	// Invalidate all other sessions, keep current one
	currentSessionID := c.Locals("sessionID").(string)
	h.Repos.Sessions.DeleteByUserIDExcept(user.ID, currentSessionID)

	return c.JSON(fiber.Map{"ok": true})
}
