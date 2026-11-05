package handler

import (
	"crypto/hmac"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"github.com/willvar/dofs"

	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
)

const maxPublicAvatarBytes = 10 * 1024 * 1024

// --- helpers ---

func (h *Handler) issueSession(w http.ResponseWriter, r *http.Request, user *model.User, method string) error {
	h.Challenges.ClearLoginAttempts(httpx.ClientIP(r), method, user.Username)
	h.Audit.Log(user.ID, user.Username, httpx.ClientIP(r), "login", "", method, "success", 0)

	sessionID, err := h.Repos.Sessions.Create(user.ID, user.Username, user.Role)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "session_creation_failed"})
	}

	secure := httpx.Secure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    auth.SignCookie(sessionID, h.Config.Server.SessionSecret),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7,
		Path:     "/",
	})

	needsSetup := user.Email == "" && !user.TOTPEnabled
	avatarEndpoint := h.publicAvatarEndpoint(user)
	historyLimit := h.historyLimit(r, user.ID)

	return httpx.JSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":              user.ID,
			"username":        user.Username,
			"display_name":    user.DisplayName,
			"role":            user.Role,
			"email":           user.Email,
			"totp_enabled":    user.TOTPEnabled,
			"avatar_endpoint": avatarEndpoint,
			"history_limit":   historyLimit,
		},
		"needs_setup": needsSetup,
	})
}

func (h *Handler) historyLimit(r *http.Request, userID string) int64 {
	if h.DOFS == nil {
		return dofs.DefaultHistoryLimit
	}
	limit, err := h.DOFS.HistoryLimit(r.Context(), userID)
	if err != nil || limit < dofs.DefaultHistoryLimit {
		return dofs.DefaultHistoryLimit
	}
	return limit
}

func (h *Handler) checkLocked(w http.ResponseWriter, r *http.Request, method, username string) bool {
	if h.Challenges.CheckLoginLocked(httpx.ClientIP(r), method, username) {
		_ = httpx.JSON(w, 429, map[string]any{
			"error":       "too_many_attempts",
			"retry_after": int(auth.LockoutDuration.Seconds()),
		})
		return true
	}
	return false
}

func (h *Handler) loginFail(w http.ResponseWriter, r *http.Request, method, username string) error {
	if h.Challenges.RecordLoginFailure(httpx.ClientIP(r), method, username) {
		h.Audit.Log("", username, httpx.ClientIP(r), "login_locked", "", method, "fail", 0)
		return httpx.JSON(w, 429, map[string]any{
			"error":       "too_many_attempts",
			"retry_after": int(auth.LockoutDuration.Seconds()),
		})
	}
	h.Audit.Log("", username, httpx.ClientIP(r), "login_fail", "", method, "fail", 0)
	return httpx.JSON(w, 401, map[string]any{"error": "invalid_credentials"})
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

func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
		Method   string `json:"method"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	if body.Username == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "username_required"})
	}

	switch {
	case body.Password != "":
		return h.verifyPassword(w, r, body.Username, body.Password)
	case body.OTP != "":
		return h.verifyOTP(w, r, body.Username, body.OTP)
	case body.Method == "email":
		return h.verifyEmailRequest(w, r, body.Username)
	default:
		return httpx.JSON(w, 400, map[string]any{"error": "credentials_required"})
	}
}

func (h *Handler) verifyPassword(w http.ResponseWriter, r *http.Request, username, password string) error {
	if h.checkLocked(w, r, "password", username) {
		return nil
	}

	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil || !model.CheckPassword(user.PasswordHash, password) {
		return h.loginFail(w, r, "password", username)
	}

	hasEmail := user.Email != ""
	hasOTP := user.TOTPEnabled

	if !hasEmail && !hasOTP {
		// No 2FA — direct token
		token, err := h.createChallengeForUser(user, nil, "")
		if err != nil {
			return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
		}
		return httpx.JSON(w, http.StatusOK, map[string]any{"token": token, "methods": []string{}})
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
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"token": token, "methods": methods})
}

func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request, username, code string) error {
	if h.checkLocked(w, r, "otp", username) {
		return nil
	}

	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil || !user.TOTPEnabled {
		return h.loginFail(w, r, "otp", username)
	}

	if h.Challenges.IsOTPUsed(user.ID, code) {
		return httpx.JSON(w, 401, map[string]any{"error": "otp_already_used"})
	}

	if !auth.ValidateTOTP(user.TOTPSecret, code) {
		return h.loginFail(w, r, "otp", username)
	}

	h.Challenges.MarkOTPUsed(user.ID, code)

	token, err := h.createChallengeForUser(user, nil, "")
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"token": token, "methods": []string{}})
}

func (h *Handler) verifyEmailRequest(w http.ResponseWriter, r *http.Request, username string) error {
	// Anti-enumeration: always return same shape
	dummyResponse := map[string]any{"token": "dummy", "methods": []string{"email"}}

	if h.Challenges.CheckLoginLocked(httpx.ClientIP(r), "email", username) {
		return httpx.JSON(w, http.StatusOK, dummyResponse)
	}

	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil || user.Email == "" {
		return httpx.JSON(w, http.StatusOK, dummyResponse)
	}

	emailCode := service.GenerateEmailCode()
	token, err := h.createChallengeForUser(user, []string{"email"}, emailCode)
	if err != nil {
		return httpx.JSON(w, http.StatusOK, dummyResponse)
	}

	go func() {
		if err := h.Email.SendVerification(user.Email, emailCode); err != nil {
			log.Printf("[auth] failed to send login email to %s: %v", user.Email, err)
		}
	}()

	return httpx.JSON(w, http.StatusOK, map[string]any{"token": token, "methods": []string{"email"}})
}

// --- POST /auth/login ---

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Token  string `json:"token"`
		Code   string `json:"code"`
		Method string `json:"method"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}

	challenge := h.Challenges.GetLoginChallenge(body.Token)
	if challenge == nil {
		return httpx.JSON(w, 401, map[string]any{"error": "token_expired"})
	}

	hasMethods := len(challenge.Methods) > 0

	if !hasMethods {
		// Direct login — no code needed
		if body.Code != "" {
			return httpx.JSON(w, 400, map[string]any{"error": "code_not_expected"})
		}
		h.Challenges.DeleteLoginChallenge(body.Token)
		user, err := h.Repos.Users.GetByID(challenge.UserID)
		if err != nil {
			return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
		}
		return h.issueSession(w, r, user, "password")
	}

	// 2FA — code required
	if body.Code == "" || body.Method == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "code_required"})
	}

	// Check lockout
	if h.checkLocked(w, r, body.Method, challenge.Username) {
		return nil
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
		return httpx.JSON(w, 400, map[string]any{"error": "method_not_available"})
	}

	user, err := h.Repos.Users.GetByID(challenge.UserID)
	if err != nil {
		h.Challenges.DeleteLoginChallenge(body.Token)
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}

	switch body.Method {
	case "email":
		if !hmac.Equal([]byte(challenge.EmailCode), []byte(body.Code)) {
			if h.Challenges.RecordLoginFailure(httpx.ClientIP(r), "email", challenge.Username) {
				return httpx.JSON(w, 429, map[string]any{"error": "too_many_attempts", "retry_after": int(auth.LockoutDuration.Seconds())})
			}
			return httpx.JSON(w, 401, map[string]any{"error": "invalid_code"})
		}
	case "otp":
		if h.Challenges.IsOTPUsed(user.ID, body.Code) {
			return httpx.JSON(w, 401, map[string]any{"error": "otp_already_used"})
		}
		if !auth.ValidateTOTP(user.TOTPSecret, body.Code) {
			if h.Challenges.RecordLoginFailure(httpx.ClientIP(r), "otp", challenge.Username) {
				return httpx.JSON(w, 429, map[string]any{"error": "too_many_attempts", "retry_after": int(auth.LockoutDuration.Seconds())})
			}
			return httpx.JSON(w, 401, map[string]any{"error": "invalid_code"})
		}
		h.Challenges.MarkOTPUsed(user.ID, body.Code)
	default:
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_method"})
	}

	h.Challenges.DeleteLoginChallenge(body.Token)
	return h.issueSession(w, r, user, body.Method)
}

// --- GET /auth/config ---

func (h *Handler) handleAuthConfig(w http.ResponseWriter, r *http.Request) error {
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"smtp_enabled": h.Email.Configured(),
	})
}

// --- POST /auth/logout ---

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	h.Audit.Log(session.UserID, session.Username, httpx.ClientIP(r), "logout", "", "", "success", 0)

	sessionID := middleware.SessionID(r)
	h.Repos.Sessions.Delete(sessionID)

	secure := httpx.Secure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Path:     "/",
	})
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.LegacySessionCookieName,
		Value:    "",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Path:     "/",
	})

	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- GET /auth/me ---

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	user, err := h.Repos.Users.GetByID(session.UserID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "user_not_found"})
	}

	avatarEndpoint := h.publicAvatarEndpoint(user)
	historyLimit := h.historyLimit(r, user.ID)

	return httpx.JSON(w, http.StatusOK, map[string]any{
		"id":              user.ID,
		"username":        user.Username,
		"display_name":    user.DisplayName,
		"role":            user.Role,
		"email":           user.Email,
		"totp_enabled":    user.TOTPEnabled,
		"avatar_endpoint": avatarEndpoint,
		"history_limit":   historyLimit,
	})
}

func (h *Handler) publicAvatarEndpoint(user *model.User) string {
	avatarPath := "/.domus/user/avatar.webp"
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
func (h *Handler) handlePublicAvatar(w http.ResponseWriter, r *http.Request) error {
	username := r.PathValue("username")
	if username == "" {
		return httpx.JSON(w, 400, map[string]any{"error": "missing_username"})
	}

	// Look up avatar file record
	user, err := h.Repos.Users.GetByUsername(username)
	if err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "no_avatar"})
	}
	fileRecord, err := h.Repos.Files.Get(user.ID, "/.domus/user/avatar.webp")
	if err != nil || fileRecord.Status != "ready" || fileRecord.WrappedDEK == "" ||
		fileRecord.Size <= 0 || fileRecord.Size > maxPublicAvatarBytes {
		return httpx.JSON(w, 404, map[string]any{"error": "no_avatar"})
	}

	// Unwrap only the public avatar DEK. The ciphertext body remains on the
	// browser-to-object-store connection.
	kek, err := h.loadUserKEK(user.ID)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	defer dofs.Clear(kek)
	wrappedBytes, err := hex.DecodeString(fileRecord.WrappedDEK)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "internal_error"})
	}
	defer dofs.Clear(dek)
	presignedURL, err := h.Store.GeneratePresignedURL(fileRecord.StorageKey(), 4*time.Hour)
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "presign_failed"})
	}

	w.Header().Set("Cache-Control", "no-store")
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"url":          presignedURL,
		"dek":          hex.EncodeToString(dek),
		"size":         fileRecord.Size,
		"content_type": "image/webp",
		"chunk_size":   auth.DefaultChunkSize,
		"generation":   fileRecord.Generation,
	})
}
