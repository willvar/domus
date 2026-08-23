package middleware

import (
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"domus/internal/auth"
	"domus/internal/model"
)

const (
	SessionCookieName       = "domus_session"
	LegacySessionCookieName = "zephyr_session"
)

func sessionCookie(c *fiber.Ctx) (string, bool) {
	if value := c.Cookies(SessionCookieName); value != "" {
		return value, false
	}
	return c.Cookies(LegacySessionCookieName), true
}

func migrateLegacySessionCookie(c *fiber.Ctx, value string) {
	c.Cookie(&fiber.Cookie{
		Name: SessionCookieName, Value: value, HTTPOnly: true,
		Secure: strings.EqualFold(c.Protocol(), "https"), SameSite: "Lax",
		MaxAge: 86400 * 7, Path: "/",
	})
}

// Middleware holds dependencies for HTTP middleware.
type Middleware struct {
	Sessions      model.SessionRepo
	SessionSecret string
}

// New creates a new Middleware instance.
func New(sessions model.SessionRepo, sessionSecret string) *Middleware {
	return &Middleware{
		Sessions:      sessions,
		SessionSecret: sessionSecret,
	}
}

// AuthRequired checks for valid session with HMAC signature verification.
func (m *Middleware) AuthRequired() fiber.Handler {
	return func(c *fiber.Ctx) error {
		cookieValue, legacy := sessionCookie(c)
		if cookieValue == "" {
			return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
		}

		sessionID, err := auth.VerifyCookie(cookieValue, m.SessionSecret)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": "invalid_session"})
		}

		session := m.Sessions.Get(sessionID)
		if session == nil {
			return c.Status(401).JSON(fiber.Map{"error": "session_expired"})
		}

		c.Locals("session", session)
		c.Locals("sessionID", sessionID)
		if legacy {
			migrateLegacySessionCookie(c, cookieValue)
		}
		return c.Next()
	}
}

// RoleRequired checks that user has one of the allowed roles.
func RoleRequired(roles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		session := c.Locals("session").(*model.Session)
		for _, r := range roles {
			if session.Role == r {
				return c.Next()
			}
		}
		return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
	}
}

// RootRequired restricts to admin only.
func RootRequired() fiber.Handler {
	return RoleRequired("root")
}

// WebSocketUpgrade validates the session cookie during the HTTP upgrade request
// and rejects unauthorized connections before the WebSocket handshake completes.
func (m *Middleware) WebSocketUpgrade() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}

		cookieValue, legacy := sessionCookie(c)
		if cookieValue == "" {
			return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
		}

		sessionID, err := auth.VerifyCookie(cookieValue, m.SessionSecret)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": "invalid_session"})
		}

		session := m.Sessions.Get(sessionID)
		if session == nil {
			return c.Status(401).JSON(fiber.Map{"error": "session_expired"})
		}

		c.Locals("session", session)
		c.Locals("sessionID", sessionID)
		if legacy {
			migrateLegacySessionCookie(c, cookieValue)
		}
		return c.Next()
	}
}

const (
	internalRootPath       = "/.domus/"
	internalUserPath       = "/.domus/user/"
	virtualPrivateUserPath = "/.user/"
)

// normalizePath converts an API path into the canonical namespace-relative
// absolute form used by the DOFS-backed file repository. A user's identity is
// deliberately absent from this path: the authenticated user ID selects the
// DOFS namespace, and "/" is that namespace's root inode.
func normalizePath(p string) (string, error) {
	if strings.ContainsRune(p, '\x00') {
		return "", fiber.NewError(fiber.StatusForbidden, "invalid path")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"
	cleaned := path.Clean(p)
	if !strings.HasPrefix(cleaned, "/") {
		return "", fiber.NewError(fiber.StatusForbidden, "invalid path")
	}
	if trailingSlash && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned, nil
}

// ResolvePath maps a public application path to a path inside the current
// user's DOFS namespace. Domus-owned internals are unreachable, while every
// other valid name (including /__trash__/) remains available to the user.
func ResolveApplicationPath(p string) (string, error) {
	cleaned, err := normalizePath(p)
	if err != nil {
		return "", err
	}
	if cleaned == strings.TrimSuffix(internalRootPath, "/") || strings.HasPrefix(cleaned, internalRootPath) ||
		cleaned == strings.TrimSuffix(virtualPrivateUserPath, "/") || strings.HasPrefix(cleaned, virtualPrivateUserPath) {
		return "", fiber.NewError(fiber.StatusForbidden, "reserved path")
	}
	return cleaned, nil
}

func ResolvePath(_ *fiber.Ctx, p string) (string, error) {
	return ResolveApplicationPath(p)
}

// ResolveInternalPath maps Domus-owned virtual paths into the reserved area of
// the namespace. It is used only by explicitly internal upload/read requests.
func ResolveInternalApplicationPath(p string) (string, error) {
	cleaned, err := normalizePath(p)
	if err != nil {
		return "", err
	}
	if cleaned == strings.TrimSuffix(virtualPrivateUserPath, "/") {
		return internalUserPath, nil
	}
	if strings.HasPrefix(cleaned, virtualPrivateUserPath) {
		relative := strings.TrimPrefix(cleaned, virtualPrivateUserPath)
		if relative == "thumbnails" || strings.HasPrefix(relative, "thumbnails/") {
			return "/.domus/" + relative, nil
		}
		return internalUserPath + relative, nil
	}
	return "", fiber.NewError(fiber.StatusForbidden, "invalid internal path")
}

func ResolveInternalPath(_ *fiber.Ctx, p string) (string, error) {
	return ResolveInternalApplicationPath(p)
}

// ToAppPath converts a stored namespace path back into its public application
// representation. Internal paths have no public path representation.
func ToAppPath(storagePath, _ string) string {
	if IsInternalStoragePath(storagePath) {
		return ""
	}
	if !strings.HasPrefix(storagePath, "/") {
		return "/" + storagePath
	}
	return storagePath
}

// IsInternalStoragePath reports whether a canonical repository path belongs to
// Domus rather than to the user's visible file tree.
func IsInternalStoragePath(storagePath string) bool {
	return storagePath == strings.TrimSuffix(internalRootPath, "/") || strings.HasPrefix(storagePath, internalRootPath)
}
