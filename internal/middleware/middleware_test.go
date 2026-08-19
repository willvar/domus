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
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/" {
		t.Fatalf("expected namespace root, got %q", body)
	}
}

func TestResolvePath_UserValidPath(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=alice&role=user&path=docs/file.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/docs/file.txt" {
		t.Fatalf("expected canonical namespace path, got %q", body)
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
	if string(body) != "/bob/secret.txt" {
		t.Fatalf("expected normalized path /bob/secret.txt, got %s", body)
	}
}

func TestResolvePath_IdentityComesFromSessionNotPath(t *testing.T) {
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
	if string(body) != "/bob/file.txt" {
		t.Fatalf("expected namespace-relative path /bob/file.txt, got %s", body)
	}
}

func TestResolvePath_RoleDoesNotChangeNamespaceRoot(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=admin&role=admin&path=", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/" {
		t.Fatalf("expected namespace root, got %q", body)
	}
}

func TestResolvePath_PathSegmentIsNotATenantSelector(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?username=admin&role=admin&path=alice/file.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/alice/file.txt" {
		t.Fatalf("expected ordinary namespace path /alice/file.txt, got %q", body)
	}
}

func TestResolvePath_MapsVirtualTrashToReservedStorage(t *testing.T) {
	app := resolvePathTestApp()
	req := httptest.NewRequest("GET", "/resolve?path=/__trash__/docs/file.txt", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "/.domus/trash/docs/file.txt" {
		t.Fatalf("trash mapping status=%d body=%q", resp.StatusCode, body)
	}
	if got := ToAppPath(string(body), "ignored"); got != "/__trash__/docs/file.txt" {
		t.Fatalf("trash reverse mapping = %q", got)
	}
}

func TestResolvePath_RejectsPrivateStorage(t *testing.T) {
	app := resolvePathTestApp()
	for _, requested := range []string{"/.domus/", "/.domus/trash/a", "/.user/preferences.json"} {
		req := httptest.NewRequest("GET", "/resolve?path="+requested, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("path %q status=%d, want 403", requested, resp.StatusCode)
		}
	}
}

func TestResolveInternalPath_MapsPrivateUserFiles(t *testing.T) {
	got, err := ResolveInternalApplicationPath("/.user/preferences.json")
	if err != nil || got != "/.domus/user/preferences.json" {
		t.Fatalf("private user path = %q, %v", got, err)
	}
	got, err = ResolveInternalApplicationPath("/.user/thumbnails/thumb.webp")
	if err != nil || got != "/.domus/thumbnails/thumb.webp" {
		t.Fatalf("thumbnail path = %q, %v", got, err)
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
