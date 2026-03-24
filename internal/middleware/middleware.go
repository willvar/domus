package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/auth"
	"zephyr/internal/model"
)

const SessionCookieName = "zephyr_session"

// Middleware holds dependencies for HTTP middleware.
type Middleware struct {
	Sessions      *model.SessionStore
	SessionSecret string
}

// New creates a new Middleware instance.
func New(sessions *model.SessionStore, sessionSecret string) *Middleware {
	return &Middleware{
		Sessions:      sessions,
		SessionSecret: sessionSecret,
	}
}

// AuthRequired checks for valid session with HMAC signature verification.
func (m *Middleware) AuthRequired() fiber.Handler {
	return func(c *fiber.Ctx) error {
		cookieValue := c.Cookies(SessionCookieName)
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

// PermissionRequired checks that the user has the given permission bit(s).
// Admin role always passes regardless of bitmask.
func PermissionRequired(perm int64) fiber.Handler {
	return func(c *fiber.Ctx) error {
		session := c.Locals("session").(*model.Session)
		if session.Role == "admin" {
			return c.Next()
		}
		if session.Permissions&perm == perm {
			return c.Next()
		}
		return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
	}
}

// AdminRequired restricts to admin only.
func AdminRequired() fiber.Handler {
	return RoleRequired("admin")
}

// ResolvePath resolves the user-relative path to the full OSS key
// and validates that the user has access to the path.
func ResolvePath(c *fiber.Ctx, path string) (string, error) {
	session := c.Locals("session").(*model.Session)

	path = strings.TrimPrefix(path, "/")

	if session.Role == "admin" {
		if path == "" {
			return session.Username + "/", nil
		}
		return path, nil
	}

	prefix := session.Username + "/"
	if path == "" {
		return prefix, nil
	}

	if !strings.HasPrefix(path, prefix) {
		return "", fiber.NewError(403, "access denied: path outside your space")
	}

	if strings.Contains(path, "..") {
		return "", fiber.NewError(403, "invalid path")
	}

	return path, nil
}
