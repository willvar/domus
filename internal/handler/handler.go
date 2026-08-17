package handler

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
	"domus/internal/store"
	"domus/internal/terminal"
	workspaceRuntime "domus/internal/workspace"
	"domus/internal/ws"
)

// avatarCacheMaxBytes is the total memory budget for the decrypted avatar LRU cache.
const avatarCacheMaxBytes = 50 * 1024 * 1024 // 50 MB

// avatarEntry holds a decrypted avatar image in the LRU cache.
type avatarEntry struct {
	data []byte
}

// Handler holds all dependencies for HTTP handlers.
type Handler struct {
	Config     *config.Config
	Repos      *model.Repos
	Store      store.FileStore
	Email      service.EmailSender
	Audit      *model.AuditWorker
	Challenges *auth.ChallengeManager
	Mid        *middleware.Middleware
	Hub        *ws.Hub
	Terminal   *terminal.Manager
	Workspace  workspaceRuntime.Service

	avatarCache *lru.Cache[string, *avatarEntry]

	mediaMu         sync.Mutex
	mediaJobs       map[string]context.CancelFunc
	mediaJobUsers   map[string]string
	mediaJobsByUser map[string]int
	mediaIdleByUser map[string]chan struct{}
	mediaBlocked    map[string]bool
	mediaWG         sync.WaitGroup
	mediaClosing    bool
}

// RegisterRoutes registers all API routes on the Fiber app.
func (h *Handler) RegisterRoutes(app *fiber.App) {
	if h.Workspace == nil {
		panic("handler: workspace service is required")
	}
	if h.Terminal == nil {
		panic("handler: terminal manager is required")
	}
	if h.Hub == nil {
		h.Hub = ws.NewHub()
	}

	// Initialize avatar LRU cache (max 500 entries; byte-budget enforced on evict)
	if h.avatarCache == nil {
		h.avatarCache, _ = lru.New[string, *avatarEntry](500)
	}

	// /auth (public)
	app.Get("/auth", h.handleAuthConfig)
	app.Post("/auth", h.handleLogin)
	app.Post("/auth/verify", h.handleVerify)
	app.Get("/user/avatar/:username", h.handlePublicAvatar)

	authed := app.Group("", h.Mid.AuthRequired())
	authed.Delete("/auth", h.handleLogout)

	// /user
	user := authed.Group("/user")
	user.Get("/", h.handleMe)
	user.Get("/storage", h.handleStorageUsage)
	user.Get("/security", h.handleSecurityStatus)
	user.Put("/display-name", h.handleUpdateDisplayName)
	user.Put("/security/password", h.handleChangePassword)
	user.Post("/security/email/bind", h.handleBindEmail)
	user.Post("/security/email/verify", h.handleVerifyBindEmail)
	user.Delete("/security/email", h.handleUnbindEmail)
	user.Post("/security/otp/setup", h.handleOTPSetup)
	user.Post("/security/otp/enable", h.handleOTPEnable)
	user.Delete("/security/otp", h.handleOTPDisable)

	// /audit
	audit := authed.Group("/audit")
	audit.Get("/", middleware.RootRequired(), h.handleListAuditLogs)
	audit.Post("/", h.handleAuditPreview)
	auditUser := audit.Group("/user", middleware.RootRequired())
	auditUser.Get("/", h.handleListUsers)
	auditUser.Post("/", h.handleCreateUser)
	auditUser.Put("/:id", h.handleUpdateUser)
	auditUser.Delete("/:id", h.handleDeleteUser)
	auditUser.Delete("/:id/otp", h.handleResetUserOTP)
	auditUser.Delete("/:id/email", h.handleResetUserEmail)

	// /admin (root only)
	admin := authed.Group("/admin", middleware.RootRequired())
	admin.Get("/oss/cors-check", h.handleCORSCheck)

	// /file
	file := authed.Group("/file")
	file.Get("/", h.handleList)
	file.Get("/search", h.handleSearch)
	file.Get("/access", h.handleFileAccess)
	file.Put("/content/diff", h.handlePatchContent)
	file.Put("/shared/:share_id/content/diff", h.handleSharePatchContent)
	file.Post("/transcode", h.handleTranscode)
	file.Post("/mkdir", h.handleMkdir)
	file.Post("/rename", h.handleRename)
	file.Post("/copy", h.handleCopy)
	file.Post("/move", h.handleMove)
	file.Delete("/delete", h.handleDelete)

	fileUpload := file.Group("/upload")
	fileUpload.Get("/", h.handleUploadStatus)
	fileUpload.Post("/", h.handleUploadDispatch)
	fileUpload.Get("/presign", h.handleUploadPresign)
	fileUpload.Post("/heartbeat", h.handleUploadHeartbeat)
	fileUpload.Post("/cancel", h.handleUploadCancel)
	fileUpload.Post("/cleanup", h.handleUploadCleanup)

	// /file/share (authenticated)
	file.Post("/share", h.handleCreateShare)
	file.Get("/share/owned", h.handleListOwnedShares)
	file.Get("/shares", h.handleListShares)
	file.Delete("/share/:id", h.handleDeleteShare)
	file.Get("/shared", h.handleListSharedWithMe)
	file.Get("/shared/:share_id", h.handleShareInfo)

	// /task
	task := authed.Group("/task")
	task.Get("/", h.handleListTasks)
	task.Delete("/done", h.handleClearDoneTasks)
	task.Delete("/:id", h.handleCancelTask)

	// /workspace
	workspace := authed.Group("/workspace")
	workspace.Get("/", h.handleWorkspaceLoad)
	workspace.Put("/", h.handleWorkspaceSave)
	workspace.Delete("/", h.handleWorkspaceClear)

	// WebSocket — auth via cookie on HTTP upgrade
	app.Use("/ws", h.Mid.WebSocketUpgrade())
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		session := c.Locals("session").(*model.Session)
		sessionID := c.Locals("sessionID").(string)
		h.Hub.HandleConnection(c.Conn, session, sessionID)
	}))

	// Register realtime/session WS actions only.
	h.registerWSActions()
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
	serverKey, err := auth.ServerKeyFromSecret(h.Config.Server.EncryptionSecret)
	if err != nil {
		return "", err
	}
	wrapped, err := auth.WrapKEK(serverKey, kek)
	if err != nil {
		return "", fmt.Errorf("wrap KEK: %w", err)
	}
	return hex.EncodeToString(wrapped), nil
}

// cloneDirFiles copies file records from srcPrefix to dstPrefix,
// preserving WrappedDEK, plaintext size, and other metadata.
func (h *Handler) cloneDirFiles(userID, srcPrefix, dstPrefix string) error {
	records, err := h.Repos.Files.ListByPrefix(userID, srcPrefix)
	if err != nil {
		return err
	}
	thumbnailCopies := make(map[string]string, len(records))
	for _, r := range records {
		if r.IsDir {
			continue
		}
		newPath := dstPrefix + strings.TrimPrefix(r.Path, srcPrefix)
		thumbnailCopies[r.StorageKey()] = newPath
	}
	type thumbnailUpdate struct {
		path, key, wrappedDEK string
		width, height         int
		duration              float64
	}
	thumbnailUpdates := make([]thumbnailUpdate, 0)
	for _, r := range records {
		newPath := dstPrefix + strings.TrimPrefix(r.Path, srcPrefix)
		newName := filepath.Base(strings.TrimSuffix(newPath, "/"))
		if !r.IsDir && r.StorageKey() != r.Path {
			if err := h.Store.CopyObject(r.StorageKey(), newPath); err != nil {
				return err
			}
		}
		var opts []model.UpsertFileOpts
		if r.WrappedDEK != "" {
			opts = append(opts, model.UpsertFileOpts{WrappedDEK: r.WrappedDEK})
		}
		if err := h.Repos.Files.Upsert(userID, newPath, newName, r.IsDir, r.Size, r.ContentType, r.ContentHash, opts...); err != nil {
			return err
		}
		if r.ThumbnailKey != "" {
			if copiedThumbnailKey, ok := thumbnailCopies[r.ThumbnailKey]; ok {
				thumbnailUpdates = append(thumbnailUpdates, thumbnailUpdate{
					path: newPath, key: copiedThumbnailKey, wrappedDEK: r.ThumbnailWrappedDEK,
					width: r.MediaWidth, height: r.MediaHeight, duration: r.MediaDuration,
				})
			}
		}
	}
	// Attach metadata only after every copied thumbnail row and object exists;
	// a partial directory copy never publishes a dangling preview pointer.
	for _, update := range thumbnailUpdates {
		if err := h.Repos.Files.UpdateThumbnail(
			userID, update.path, update.key, update.wrappedDEK,
			update.width, update.height, update.duration,
		); err != nil {
			return err
		}
	}
	return nil
}
