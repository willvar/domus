package handler

import (
	"crypto/hmac"
	"encoding/hex"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/willvar/dofs"

	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
)

const maxPublicAvatarBytes = 10 * 1024 * 1024

// --- helpers ---

func (h *Handler) issueSession(c *fiber.Ctx, user *model.User, method string) error {
	h.Challenges.ClearLoginAttempts(c.IP(), method, user.Username)
	h.Audit.Log(user.ID, user.Username, c.IP(), "login", "", method, "success", 0)

	sessionID, err := h.Repos.Sessions.Create(user.ID, user.Username, user.Role)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "session_creation_failed"})
	}

	secure := strings.EqualFold(c.Protocol(), "https")
	c.Cookie(&fiber.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    auth.SignCookie(sessionID, h.Config.Server.SessionSecret),
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		MaxAge:   86400 * 7,
		Path:     "/",
	})

	needsSetup := user.Email == "" && !user.TOTPEnabled
	avatarEndpoint := h.publicAvatarEndpoint(user)

	return c.JSON(fiber.Map{
		"user": fiber.Map{
			"id":              user.ID,
			"username":        user.Username,
			"display_name":    user.DisplayName,
			"role":            user.Role,
			"email":           user.Email,
			"totp_enabled":    user.TOTPEnabled,
			"avatar_endpoint": avatarEndpoint,
		},
		"needs_setup": needsSetup,
	})
}

func (h *Handler) checkLocked(c *fiber.Ctx, method, username string) error {
	if h.Challenges.CheckLoginLocked(c.IP(), method, username) {
		return c.Status(429).JSON(fiber.Map{
			"error":       "too_many_attempts",
			"retry_after": int(auth.LockoutDuration.Seconds()),
		})
	}
	return nil
}

func (h *Handler) loginFail(c *fiber.Ctx, method, username string) error {
	if h.Challenges.RecordLoginFailure(c.IP(), method, username) {
		h.Audit.Log("", username, c.IP(), "login_locked", "", method, "fail", 0)
		return c.Status(429).JSON(fiber.Map{
			"error":       "too_many_attempts",
			"retry_after": int(auth.LockoutDuration.Seconds()),
		})
	}
	h.Audit.Log("", username, c.IP(), "login_fail", "", method, "fail", 0)
	return c.Status(401).JSON(fiber.Map{"error": "invalid_credentials"})
}

func (h *Handler) createChallengeForUser(user *model.User, methods []string, emailCode string) (string, error) {
	return h.Challenges.CreateLoginChallenge(&auth.LoginChallenge{
		UserID:    user.ID,
		Username:  user.Username,
		Role:      user.Role,
		Methods:   methods,
		EmailCode: emailCode,
	})
}

// --- POST /auth/verify ---

func (h *Handler) handleVerify(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
		Method   string `json:"method"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.Username == "" {
		return c.Status(400).JSON(fiber.Map{"error": "username_required"})
	}

	switch {
	case body.Password != "":
		return h.verifyPassword(c, body.Username, body.Password)
	case body.OTP != "":
		return h.verifyOTP(c, body.Username, body.OTP)
	case body.Method == "email":
		return h.verifyEmailRequest(c, body.Username)
	default:
		return c.Status(400).JSON(fiber.Map{"error": "credentials_required"})
	}
}

func (h *Handler) verifyPassword(c *fiber.Ctx, username, password string) error {
	if err := h.checkLocked(c, "password", username); err != nil {
		return err
	}

	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil || !model.CheckPassword(user.PasswordHash, password) {
		return h.loginFail(c, "password", username)
	}

	hasEmail := user.Email != ""
	hasOTP := user.TOTPEnabled

	if !hasEmail && !hasOTP {
		// No 2FA — direct token
		token, err := h.createChallengeForUser(user, nil, "")
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		return c.JSON(fiber.Map{"token": token, "methods": []string{}})
	}

	// 2FA required
	var methods []string
	var emailCode string

	if hasEmail {
		methods = append(methods, "email")
		emailCode = service.GenerateEmailCode()
		go func() {
			if err := h.Email.SendVerification(user.Email, emailCode); err != nil {
				log.Printf("[auth] failed to send 2FA email to %s: %v", user.Email, err)
			}
		}()
	}
	if hasOTP {
		methods = append(methods, "otp")
	}

	token, err := h.createChallengeForUser(user, methods, emailCode)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	return c.JSON(fiber.Map{"token": token, "methods": methods})
}

func (h *Handler) verifyOTP(c *fiber.Ctx, username, code string) error {
	if err := h.checkLocked(c, "otp", username); err != nil {
		return err
	}

	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil || !user.TOTPEnabled {
		return h.loginFail(c, "otp", username)
	}

	if h.Challenges.IsOTPUsed(user.ID, code) {
		return c.Status(401).JSON(fiber.Map{"error": "otp_already_used"})
	}

	if !auth.ValidateTOTP(user.TOTPSecret, code) {
		return h.loginFail(c, "otp", username)
	}

	h.Challenges.MarkOTPUsed(user.ID, code)

	token, err := h.createChallengeForUser(user, nil, "")
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	return c.JSON(fiber.Map{"token": token, "methods": []string{}})
}

func (h *Handler) verifyEmailRequest(c *fiber.Ctx, username string) error {
	// Anti-enumeration: always return same shape
	dummyResponse := fiber.Map{"token": "dummy", "methods": []string{"email"}}

	if h.Challenges.CheckLoginLocked(c.IP(), "email", username) {
		return c.JSON(dummyResponse)
	}

	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil || user.Email == "" {
		return c.JSON(dummyResponse)
	}

	emailCode := service.GenerateEmailCode()
	token, err := h.createChallengeForUser(user, []string{"email"}, emailCode)
	if err != nil {
		return c.JSON(dummyResponse)
	}

	go func() {
		if err := h.Email.SendVerification(user.Email, emailCode); err != nil {
			log.Printf("[auth] failed to send login email to %s: %v", user.Email, err)
		}
	}()

	return c.JSON(fiber.Map{"token": token, "methods": []string{"email"}})
}

// --- POST /auth/login ---

func (h *Handler) handleLogin(c *fiber.Ctx) error {
	var body struct {
		Token  string `json:"token"`
		Code   string `json:"code"`
		Method string `json:"method"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	challenge := h.Challenges.GetLoginChallenge(body.Token)
	if challenge == nil {
		return c.Status(401).JSON(fiber.Map{"error": "token_expired"})
	}

	hasMethods := len(challenge.Methods) > 0

	if !hasMethods {
		// Direct login — no code needed
		if body.Code != "" {
			return c.Status(400).JSON(fiber.Map{"error": "code_not_expected"})
		}
		h.Challenges.DeleteLoginChallenge(body.Token)
		user, err := h.Repos.Users.GetByID(challenge.UserID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
		}
		return h.issueSession(c, user, "password")
	}

	// 2FA — code required
	if body.Code == "" || body.Method == "" {
		return c.Status(400).JSON(fiber.Map{"error": "code_required"})
	}

	// Check lockout
	if err := h.checkLocked(c, body.Method, challenge.Username); err != nil {
		return err
	}

	// Verify method is allowed
	methodAllowed := false
	for _, m := range challenge.Methods {
		if m == body.Method {
			methodAllowed = true
			break
		}
	}
	if !methodAllowed {
		return c.Status(400).JSON(fiber.Map{"error": "method_not_available"})
	}

	user, err := h.Repos.Users.GetByID(challenge.UserID)
	if err != nil {
		h.Challenges.DeleteLoginChallenge(body.Token)
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	switch body.Method {
	case "email":
		if !hmac.Equal([]byte(challenge.EmailCode), []byte(body.Code)) {
			if h.Challenges.RecordLoginFailure(c.IP(), "email", challenge.Username) {
				return c.Status(429).JSON(fiber.Map{"error": "too_many_attempts", "retry_after": int(auth.LockoutDuration.Seconds())})
			}
			return c.Status(401).JSON(fiber.Map{"error": "invalid_code"})
		}
	case "otp":
		if h.Challenges.IsOTPUsed(user.ID, body.Code) {
			return c.Status(401).JSON(fiber.Map{"error": "otp_already_used"})
		}
		if !auth.ValidateTOTP(user.TOTPSecret, body.Code) {
			if h.Challenges.RecordLoginFailure(c.IP(), "otp", challenge.Username) {
				return c.Status(429).JSON(fiber.Map{"error": "too_many_attempts", "retry_after": int(auth.LockoutDuration.Seconds())})
			}
			return c.Status(401).JSON(fiber.Map{"error": "invalid_code"})
		}
		h.Challenges.MarkOTPUsed(user.ID, body.Code)
	default:
		return c.Status(400).JSON(fiber.Map{"error": "invalid_method"})
	}

	h.Challenges.DeleteLoginChallenge(body.Token)
	return h.issueSession(c, user, body.Method)
}

// --- GET /auth/config ---

func (h *Handler) handleAuthConfig(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"smtp_enabled": h.Email.Configured(),
	})
}

// --- POST /auth/logout ---

func (h *Handler) handleLogout(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	h.Audit.Log(session.UserID, session.Username, c.IP(), "logout", "", "", "success", 0)

	sessionID := c.Locals("sessionID").(string)
	h.Repos.Sessions.Delete(sessionID)

	secure := strings.EqualFold(c.Protocol(), "https")
	c.Cookie(&fiber.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		MaxAge:   -1,
		Path:     "/",
	})
	c.Cookie(&fiber.Cookie{
		Name:     middleware.LegacySessionCookieName,
		Value:    "",
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		MaxAge:   -1,
		Path:     "/",
	})

	return c.JSON(fiber.Map{"ok": true})
}

// --- GET /auth/me ---

func (h *Handler) handleMe(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "user_not_found"})
	}

	avatarEndpoint := h.publicAvatarEndpoint(user)

	return c.JSON(fiber.Map{
		"id":              user.ID,
		"username":        user.Username,
		"display_name":    user.DisplayName,
		"role":            user.Role,
		"email":           user.Email,
		"totp_enabled":    user.TOTPEnabled,
		"avatar_endpoint": avatarEndpoint,
	})
}

func (h *Handler) publicAvatarEndpoint(user *model.User) string {
	avatarPath := user.Username + "/.user/avatar.webp"
	record, err := h.Repos.Files.Get(user.ID, avatarPath)
	if err != nil || record.Status != "ready" || record.WrappedDEK == "" ||
		record.Size <= 0 || record.Size > maxPublicAvatarBytes {
		return ""
	}
	return "/user/avatar/" + user.Username
}

// handlePublicAvatar returns only the control metadata needed for a browser to
// fetch the encrypted avatar directly from OSS and decrypt it locally. Avatars
// are intentionally public, so this endpoint exposes their per-file DEK, but
// the Domus HTTP process never reads or proxies the object body.
func (h *Handler) handlePublicAvatar(c *fiber.Ctx) error {
	username := c.Params("username")
	if username == "" {
		return c.Status(400).JSON(fiber.Map{"error": "missing_username"})
	}

	// Look up avatar file record
	avatarKey := username + "/.user/avatar.webp"
	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "no_avatar"})
	}
	fileRecord, err := h.Repos.Files.Get(user.ID, avatarKey)
	if err != nil || fileRecord.Status != "ready" || fileRecord.WrappedDEK == "" ||
		fileRecord.Size <= 0 || fileRecord.Size > maxPublicAvatarBytes {
		return c.Status(404).JSON(fiber.Map{"error": "no_avatar"})
	}

	// Unwrap only the public avatar DEK. The ciphertext body remains on the
	// browser-to-object-store connection.
	kek, err := h.loadUserKEK(user.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	defer dofs.Clear(kek)
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	defer dofs.Clear(dek)
	presignedURL, err := h.Store.GeneratePresignedURL(fileRecord.StorageKey(), 4*time.Hour)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "presign_failed"})
	}

	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{
		"url":          presignedURL,
		"dek":          hex.EncodeToString(dek),
		"size":         fileRecord.Size,
		"content_type": "image/webp",
		"chunk_size":   auth.DefaultChunkSize,
		"generation":   fileRecord.Generation,
	})
}
