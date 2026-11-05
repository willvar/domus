package middleware

import (
	"context"
	"net/http"
	"path"
	"strings"

	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/model"
)

const (
	SessionCookieName       = "domus_session"
	LegacySessionCookieName = "zephyr_session"
)

func sessionCookie(r *http.Request) (string, bool) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, false
	}
	cookie, err := r.Cookie(LegacySessionCookieName)
	if err != nil {
		return "", true
	}
	return cookie.Value, true
}

func migrateLegacySessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: value, HttpOnly: true,
		Secure: httpx.Secure(r), SameSite: http.SameSiteLaxMode,
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
func (m *Middleware) AuthRequired(next http.Handler) http.Handler {
	return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		cookieValue, legacy := sessionCookie(r)
		if cookieValue == "" {
			return httpx.JSON(w, 401, map[string]any{"error": "unauthorized"})
		}

		sessionID, err := auth.VerifyCookie(cookieValue, m.SessionSecret)
		if err != nil {
			return httpx.JSON(w, 401, map[string]any{"error": "invalid_session"})
		}

		session := m.Sessions.Get(sessionID)
		if session == nil {
			return httpx.JSON(w, 401, map[string]any{"error": "session_expired"})
		}

		if legacy {
			migrateLegacySessionCookie(w, r, cookieValue)
		}
		next.ServeHTTP(w, WithSession(r, session, sessionID))
		return nil
	})
}

type sessionKey struct{}
type sessionIDKey struct{}

func WithSession(r *http.Request, session *model.Session, id string) *http.Request {
	ctx := context.WithValue(r.Context(), sessionKey{}, session)
	return r.WithContext(context.WithValue(ctx, sessionIDKey{}, id))
}

func Session(r *http.Request) *model.Session {
	session, _ := r.Context().Value(sessionKey{}).(*model.Session)
	return session
}

func SessionID(r *http.Request) string {
	id, _ := r.Context().Value(sessionIDKey{}).(string)
	return id
}

// RoleRequired checks that user has one of the allowed roles.
func RoleRequired(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			session := Session(r)
			for _, role := range roles {
				if session != nil && session.Role == role {
					next.ServeHTTP(w, r)
					return nil
				}
			}
			return httpx.JSON(w, 403, map[string]any{"error": "forbidden"})
		})
	}
}

// RootRequired restricts to admin only.
func RootRequired(next http.Handler) http.Handler {
	return RoleRequired("root")(next)
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
		return "", httpx.NewError(http.StatusForbidden, "invalid path")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"
	cleaned := path.Clean(p)
	if !strings.HasPrefix(cleaned, "/") {
		return "", httpx.NewError(http.StatusForbidden, "invalid path")
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
		return "", httpx.NewError(http.StatusForbidden, "reserved path")
	}
	return cleaned, nil
}

func ResolvePath(_ *http.Request, p string) (string, error) {
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
	return "", httpx.NewError(http.StatusForbidden, "invalid internal path")
}

func ResolveInternalPath(_ *http.Request, p string) (string, error) {
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
