package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"zephyr/internal/auth"
	"zephyr/internal/model"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password= dbname=zephyr_test sslmode=disable"
	}
	testDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("skipping test: could not connect to PostgreSQL: %v", err)
	}
	if err := testDB.AutoMigrate(&model.DBSession{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	testDB.Exec("DELETE FROM sessions")
	return testDB
}

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
	testDB := setupTestDB(t)
	sessions := model.NewSessionStore(testDB)
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
	testDB := setupTestDB(t)
	sessions := model.NewSessionStore(testDB)
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
	testDB := setupTestDB(t)
	sessions := model.NewSessionStore(testDB)
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
