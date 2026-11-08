package handler

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/willvar/dofs"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/dav"
	"domus/internal/dofsbridge"
	"domus/internal/fileview"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/oauth"
	"domus/internal/service"
	"domus/internal/store"
	"domus/internal/ws"
)

// Handler holds all dependencies for HTTP handlers.
type Handler struct {
	OAuth           *oauth.Server
	Config          *config.Config
	Repos           *model.Repos
	Store           store.ControlStore
	Email           service.EmailSender
	Audit           *model.AuditWorker
	Challenges      *auth.ChallengeManager
	Mid             *middleware.Middleware
	Hub             *ws.Hub
	DOFS            *dofsbridge.Runtime
	FileSystem      *fileview.Repo
	relayDOFSChange func(string)
}

// RegisterRoutes registers the API on the standard HTTP mux.
func (h *Handler) RegisterRoutes(app *http.ServeMux) {
	if h.Hub == nil {
		h.Hub = ws.NewHub()
	}

	register := func(method, route string, fn httpx.Handler, authenticated, rootOnly bool) {
		var endpoint http.Handler = fn
		if strings.HasPrefix(route, "/file") || strings.HasPrefix(route, "/trash") {
			endpoint = rejectFileContentPayload(endpoint)
		}
		if rootOnly {
			endpoint = middleware.RootRequired(endpoint)
		}
		if authenticated {
			endpoint = h.Mid.AuthRequired(endpoint)
		}
		if strings.HasSuffix(route, "/") {
			app.Handle(method+" "+route+"{$}", endpoint)
			app.Handle(method+" "+strings.TrimSuffix(route, "/"), endpoint)
		} else {
			app.Handle(method+" "+route, endpoint)
		}
	}
	register("GET", "/auth", h.handleAuthConfig, false, false)
	register("POST", "/auth", h.handleLogin, false, false)
	register("POST", "/auth/verify", h.handleVerify, false, false)
	register("GET", "/user/avatar/{username}", h.handlePublicAvatar, false, false)
	register("DELETE", "/auth", h.handleLogout, true, false)
	register("GET", "/user/", h.handleMe, true, false)
	register("GET", "/user/storage", h.handleStorageUsage, true, false)
	register("GET", "/user/security", h.handleSecurityStatus, true, false)
	register("PUT", "/user/history-limit", h.handleUpdateHistoryLimit, true, false)
	register("PUT", "/user/display-name", h.handleUpdateDisplayName, true, false)
	register("PUT", "/user/security/password", h.handleChangePassword, true, false)
	register("POST", "/user/security/email/bind", h.handleBindEmail, true, false)
	register("POST", "/user/security/email/verify", h.handleVerifyBindEmail, true, false)
	register("DELETE", "/user/security/email", h.handleUnbindEmail, true, false)
	register("POST", "/user/security/otp/setup", h.handleOTPSetup, true, false)
	register("POST", "/user/security/otp/enable", h.handleOTPEnable, true, false)
	register("DELETE", "/user/security/otp", h.handleOTPDisable, true, false)
	register("GET", "/audit/", h.handleListAuditLogs, true, true)
	register("POST", "/audit/", h.handleAuditPreview, true, false)
	register("GET", "/audit/user/", h.handleListUsers, true, true)
	register("POST", "/audit/user/", h.handleCreateUser, true, true)
	register("PUT", "/audit/user/{id}", h.handleUpdateUser, true, true)
	register("DELETE", "/audit/user/{id}", h.handleDeleteUser, true, true)
	register("DELETE", "/audit/user/{id}/otp", h.handleResetUserOTP, true, true)
	register("DELETE", "/audit/user/{id}/email", h.handleResetUserEmail, true, true)
	register("GET", "/admin/oss/cors-check", h.handleCORSCheck, true, true)
	register("GET", "/file/", h.handleList, true, false)
	register("GET", "/file/search", h.handleSearch, true, false)
	register("GET", "/file/access", h.handleFileAccess, true, false)
	register("GET", "/file/history", h.handleFileHistory, true, false)
	register("GET", "/file/history/access", h.handleFileHistoryAccess, true, false)
	register("POST", "/file/history/restore", h.handleFileHistoryRestore, true, false)
	register("POST", "/file/mkdir", h.handleMkdir, true, false)
	register("POST", "/file/rename", h.handleRename, true, false)
	register("POST", "/file/copy", h.handleCopy, true, false)
	register("POST", "/file/move", h.handleMove, true, false)
	register("DELETE", "/file/delete", h.handleDelete, true, false)
	register("GET", "/file/upload/", h.handleUploadStatus, true, false)
	register("POST", "/file/upload/", h.handleUploadDispatch, true, false)
	register("GET", "/file/upload/presign", h.handleUploadPresign, true, false)
	register("POST", "/file/upload/heartbeat", h.handleUploadHeartbeat, true, false)
	register("POST", "/file/upload/cancel", h.handleUploadCancel, true, false)
	register("POST", "/file/upload/cleanup", h.handleUploadCleanup, true, false)
	register("POST", "/file/transcode", h.handleTranscodeRequest, true, false)
	register("POST", "/file/thumbnail", h.handleRetryThumbnail, true, false)
	register("GET", "/file/renditions", h.handleListRenditions, true, false)
	register("POST", "/trash/", h.handleCreateTrashEntry, true, false)
	register("GET", "/trash/", h.handleListTrash, true, false)
	register("DELETE", "/trash/", h.handleEmptyTrash, true, false)
	register("GET", "/trash/{id}/list", h.handleListTrashDirectory, true, false)
	register("GET", "/trash/{id}/access", h.handleTrashAccess, true, false)
	register("GET", "/trash/{id}/history", h.handleTrashHistory, true, false)
	register("GET", "/trash/{id}/history/access", h.handleTrashHistoryAccess, true, false)
	register("POST", "/trash/{id}/history/restore", h.handleTrashHistoryRestore, true, false)
	register("POST", "/trash/{id}/restore", h.handleRestoreTrash, true, false)
	register("DELETE", "/trash/{id}", h.handleDeleteTrashItem, true, false)
	register("GET", "/task/", h.handleListTasks, true, false)
	register("DELETE", "/task/done", h.handleClearDoneTasks, true, false)
	register("DELETE", "/task/{id}", h.handleCancelTask, true, false)
	register("POST", "/task/{id}/retry", h.handleRetryTask, true, false)
	register("GET", "/ws", h.handleWebSocket, true, false)
	app.Handle("/dav", httpx.Handler(h.handleDAV))
	app.Handle("/dav/", httpx.Handler(h.handleDAV))
	if h.OAuth != nil {
		h.OAuth.RegisterRoutes(app, h.Mid)
	} else {
		app.Handle("/oauth/", httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			return httpx.NewError(http.StatusServiceUnavailable, "oauth_not_configured")
		}))
	}
	for _, prefix := range []string{"/file/", "/trash/"} {
		app.Handle(prefix, h.Mid.AuthRequired(rejectFileContentPayload(httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			return httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		}))))
	}

	// Register realtime/session WS actions only.
	h.registerWSActions()
}

func (h *Handler) handleDAV(w http.ResponseWriter, r *http.Request) error {
	if h.OAuth == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "oauth_not_configured")
	}
	userID, err := h.OAuth.Authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="domus", error="invalid_token"`)
		return httpx.NewError(http.StatusUnauthorized, "invalid_token")
	}
	dav.ServeHTTP(w, r, h.DOFS, userID)
	return nil
}

func (h *Handler) handleWebSocket(w http.ResponseWriter, r *http.Request) error {
	if !websocket.IsWebSocketUpgrade(r) {
		return httpx.NewError(http.StatusUpgradeRequired, "upgrade_required")
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: h.webSocketOriginAllowed}
	connection, err := upgrader.Upgrade(w, r, w.Header())
	if err != nil {
		return nil
	} // Upgrader has already written the HTTP error.
	h.Hub.HandleConnection(connection, middleware.Session(r), middleware.SessionID(r))
	return nil
}

func (h *Handler) webSocketOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	} // Non-browser clients still need the session cookie.
	if h.Config != nil && httpx.AllowedOrigin(origin, h.Config.Server.CORSOrigins) {
		return true
	}
	uri, err := url.Parse(origin)
	return err == nil && uri.User == nil && uri.Path == "" && uri.RawQuery == "" && uri.Fragment == "" &&
		(uri.Scheme == "http" || uri.Scheme == "https") && strings.EqualFold(uri.Host, r.Host)
}

func (h *Handler) audit(r *http.Request, action, resource, detail, status string, duration int64) {
	session := middleware.Session(r)
	if session == nil {
		return
	}
	h.Audit.Log(session.UserID, session.Username, httpx.ClientIP(r), action, resource, detail, status, duration)
}

// getFileEncryptionKey returns the cached KEK for the current user's files.
func (h *Handler) getFileEncryptionKey(session *model.Session) ([]byte, error) {
	if session.KEK != nil {
		return session.KEK, nil
	}
	return h.loadUserKEK(session.UserID)
}

// loadUserKEK loads and unwraps a user's KEK from the database.
// Used by background jobs (no session) and for target user KEK in sharing.
func (h *Handler) loadUserKEK(userID string) ([]byte, error) {
	wrappedHex, err := h.Repos.Users.GetWrappedKEK(userID)
	if err != nil {
		return nil, fmt.Errorf("load wrapped KEK: %w", err)
	}
	if wrappedHex == "" {
		return nil, fmt.Errorf("user %s has no wrapped KEK", userID)
	}
	serverKey, err := auth.ServerKeyFromSecret(h.Config.Server.EncryptionSecret)
	if err != nil {
		return nil, err
	}
	defer dofs.Clear(serverKey)
	wrappedBytes, err := hex.DecodeString(wrappedHex)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped KEK: %w", err)
	}
	return auth.UnwrapKEK(serverKey, wrappedBytes)
}

// generateWrappedKEK creates a new random KEK and wraps it with the server key.
// Returns the hex-encoded wrapped KEK for storage in the users table.
func (h *Handler) generateWrappedKEK() (string, error) {
	kek, err := auth.GenerateKEK()
	if err != nil {
		return "", fmt.Errorf("generate KEK: %w", err)
	}
	defer dofs.Clear(kek)
	serverKey, err := auth.ServerKeyFromSecret(h.Config.Server.EncryptionSecret)
	if err != nil {
		return "", err
	}
	defer dofs.Clear(serverKey)
	wrapped, err := auth.WrapKEK(serverKey, kek)
	if err != nil {
		return "", fmt.Errorf("wrap KEK: %w", err)
	}
	return hex.EncodeToString(wrapped), nil
}
