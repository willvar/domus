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

// ResolvePath converts an application-layer path (e.g. "/home/tom/file.txt")
// to the full OSS key (e.g. "tom/home/tom/file.txt").
// All users (including root) share the same logic.
func ResolvePath(c *fiber.Ctx, p string) (string, error) {
	session := c.Locals("session").(*model.Session)

	// Normalize: ensure leading /
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	// Preserve trailing slash (directory marker) since path.Clean strips it
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"

	// Clean the path (resolves /../, /./ , double slashes)
	cleaned := path.Clean(p)

	// After cleaning, reject if still contains ..
	if strings.Contains(cleaned, "..") {
		return "", fiber.NewError(403, "invalid path")
	}

	if !strings.HasPrefix(cleaned, "/") {
		return "", fiber.NewError(403, "invalid path")
	}

	if trailingSlash {
		cleaned += "/"
	}

	// Prepend OSS namespace prefix
	return session.Username + cleaned, nil
}

// ToAppPath converts an OSS key back to an application-layer path.
// e.g. "tom/home/tom/file.txt" → "/home/tom/file.txt"
func ToAppPath(ossPath, username string) string {
	return "/" + strings.TrimPrefix(ossPath, username+"/")
}
