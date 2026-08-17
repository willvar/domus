package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"domus/internal/auth"
	"domus/internal/model"
)

// mockSessionRepo is a minimal in-memory session store for middleware tests.
type mockSessionRepo struct {
	sessions map[string]*model.Session
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{sessions: make(map[string]*model.Session)}
}

func (m *mockSessionRepo) Create(userID, username, role string) (string, error) {
	id := "sess-" + username
	m.sessions[id] = &model.Session{UserID: userID, Username: username, Role: role}
	return id, nil
}

func (m *mockSessionRepo) Get(id string) *model.Session { return m.sessions[id] }

func (m *mockSessionRepo) Delete(id string) { delete(m.sessions, id) }

func (m *mockSessionRepo) DeleteByUserID(string) {}

func (m *mockSessionRepo) DeleteByUserIDExcept(string, string) {}

func (m *mockSessionRepo) CleanExpired() {}

func (m *mockSessionRepo) SetPopulateKEK(func(*model.Session)) {}

func resolvePathTestApp() *fiber.App {
	app := fiber.New()
	app.Get("/resolve", func(c *fiber.Ctx) error {
		role := c.Query("role", "user")
		username := c.Query("username", "alice")
		c.Locals("session", &model.Session{
			UserID: "test-user-id", Username: username, Role: role,
		})
		path := c.Query("path")
		resolved, err := ResolvePath(c, path)
		if err != nil {
			return err
		}
		return c.SendString(resolved)
	})
	return app
}

func TestResolvePath_UserEmptyPath(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=alice&role=user&path=", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestResolvePath_UserValidPath(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=alice&role=user&path=alice/docs/file.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestResolvePath_UserPathTraversalNormalized(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=alice&role=user&path=alice/../bob/secret.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "alice/bob/secret.txt" {
		t.Fatalf("expected normalized path alice/bob/secret.txt, got %s", body)
	}
}

func TestResolvePath_UserPathAlwaysNamespaced(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=alice&role=user&path=bob/file.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "alice/bob/file.txt" {
		t.Fatalf("expected namespaced path alice/bob/file.txt, got %s", body)
	}
}

func TestResolvePath_AdminEmptyPath(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=admin&role=admin&path=", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestResolvePath_AdminCrossUser(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=admin&role=admin&path=alice/file.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for admin cross-user, got %d", resp.StatusCode)
	}
}

func TestAuthRequired_NoCookie(t *testing.T) {
	sessions := newMockSessionRepo()
	m := New(sessions, "test")

	app := fiber.New()
	app.Get("/protected", m.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthRequired_InvalidSignature(t *testing.T) {
	sessions := newMockSessionRepo()
	m := New(sessions, "test")

	app := fiber.New()
	app.Get("/protected", m.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "fake-id.fake-sig"})
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthRequired_ValidSession(t *testing.T) {
	sessions := newMockSessionRepo()
	m := New(sessions, "test")

	sessionID, _ := sessions.Create("test-user-id", "alice", "root")
	cookie := auth.SignCookie(sessionID, "test")

	app := fiber.New()
	app.Get("/protected", m.AuthRequired(), func(c *fiber.Ctx) error {
		session := c.Locals("session").(*model.Session)
		return c.SendString(session.Username)
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookie})
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAuthRequiredMigratesLegacySessionCookie(t *testing.T) {
	sessions := newMockSessionRepo()
	m := New(sessions, "test")
	sessionID, _ := sessions.Create("test-user-id", "alice", "user")
	cookie := auth.SignCookie(sessionID, "test")

	app := fiber.New()
	app.Get("/protected", m.AuthRequired(), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})
	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: LegacySessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	found := false
	for _, responseCookie := range resp.Cookies() {
		if responseCookie.Name == SessionCookieName && responseCookie.Value == cookie {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy session was not migrated to domus_session")
	}
}
