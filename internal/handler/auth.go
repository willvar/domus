package handler

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/service"
)

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

	return c.JSON(fiber.Map{
		"user": fiber.Map{
			"id":           user.ID,
			"username":     user.Username,
			"role":         user.Role,
			"email":        user.Email,
			"totp_enabled": user.TOTPEnabled,
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

	return c.JSON(fiber.Map{"ok": true})
}

// --- GET /auth/me ---

func (h *Handler) handleMe(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "user_not_found"})
	}

	avatarKey := user.Username + "/.user/avatar.webp"
	var avatarURL string
	if fr, err := h.Repos.Files.Get(user.ID, avatarKey); err == nil && fr.WrappedDEK != "" {
		avatarURL = "/user/avatar/" + user.Username
	}

	return c.JSON(fiber.Map{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"role":         user.Role,
		"email":        user.Email,
		"totp_enabled": user.TOTPEnabled,
		"avatar_url":   avatarURL,
	})
}

// handlePublicAvatar serves the decrypted avatar image for a given username (public, no auth).
// Avatars are encrypted on OSS like all other files. The server decrypts on demand with LRU caching.
func (h *Handler) handlePublicAvatar(c *fiber.Ctx) error {
	username := c.Params("username")
	if username == "" {
		return c.Status(400).JSON(fiber.Map{"error": "missing_username"})
	}

	// Check LRU cache
	if h.avatarCache != nil {
		if entry, ok := h.avatarCache.Get(username); ok {
			c.Set("Content-Type", "image/webp")
			c.Set("Cache-Control", "public, max-age=3600")
			return c.Send(entry.data)
		}
	}

	// Look up avatar file record
	avatarKey := username + "/.user/avatar.webp"
	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "no_avatar"})
	}
	fileRecord, err := h.Repos.Files.Get(user.ID, avatarKey)
	if err != nil || fileRecord.WrappedDEK == "" {
		return c.Status(404).JSON(fiber.Map{"error": "no_avatar"})
	}

	// Decrypt avatar
	kek, err := h.loadUserKEK(user.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	reader, err := h.Store.GetObjectContent(avatarKey)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "no_avatar"})
	}
	defer func() { _ = reader.Close() }()

	var buf bytes.Buffer
	if err := auth.DecryptStream(dek, reader, &buf); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "decrypt_failed"})
	}

	data := buf.Bytes()

	// Store in LRU cache
	if h.avatarCache != nil && len(data) < avatarCacheMaxBytes {
		h.avatarCache.Add(username, &avatarEntry{data: data})
	}

	c.Set("Content-Type", "image/webp")
	c.Set("Cache-Control", "public, max-age=3600")
	return c.Send(data)
}

// InvalidateAvatarCache removes a user's avatar from the LRU cache (call after avatar update).
func (h *Handler) InvalidateAvatarCache(username string) {
	if h.avatarCache != nil {
		h.avatarCache.Remove(username)
	}
}
