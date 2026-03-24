package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/store"
)

func TestLogin_Success(t *testing.T) {
	app, _ := setupTestApp(t)
	_, _ = model.CreateUser("testuser", "testpass", "admin", model.PermAll)

	verifyBody := `{"username":"testuser","password":"testpass"}`
	req := httptest.NewRequest("POST", "/auth/verify", strings.NewReader(verifyBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("verify: expected 200, got %d", resp.StatusCode)
	}

	var verifyResult map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&verifyResult)
	token, ok := verifyResult["token"].(string)
	if !ok || token == "" {
		t.Fatal("expected token from verify")
	}

	loginBody, _ := json.Marshal(map[string]string{"token": token})
	req2 := httptest.NewRequest("POST", "/auth/login", bytes.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != 200 {
		t.Fatalf("login: expected 200, got %d", resp2.StatusCode)
	}

	cookies := resp2.Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == middleware.SessionCookieName {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected session cookie to be set")
	}
}

func TestLogin_BadPassword(t *testing.T) {
	app, _ := setupTestApp(t)
	_, _ = model.CreateUser("testuser", "testpass", "admin", model.PermAll)

	body := `{"username":"testuser","password":"wrongpass"}`
	req := httptest.NewRequest("POST", "/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLogin_NonexistentUser(t *testing.T) {
	app, _ := setupTestApp(t)

	body := `{"username":"ghost","password":"pass"}`
	req := httptest.NewRequest("POST", "/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMe(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("admin", "pass")

	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["username"] != "admin" {
		t.Fatalf("expected admin, got %v", result["username"])
	}
}

func TestLogout(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("admin", "pass")

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	req2 := httptest.NewRequest("GET", "/auth/me", nil)
	req2.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != 401 {
		t.Fatalf("expected 401 after logout, got %d", resp2.StatusCode)
	}
}

func TestListUsers_AsAdmin(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("admin", "pass")

	req := httptest.NewRequest("GET", "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleList(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("admin", "pass")

	adminUser, _ := model.GetUserByUsername("admin")
	_ = model.UpsertFile(adminUser.ID, "admin/test.txt", "test.txt", false, 100, "", "")
	_ = model.UpsertFile(adminUser.ID, "admin/docs/", "docs", true, 0, "", "")

	req := httptest.NewRequest("GET", "/list?path=", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	files, ok := result["files"].([]interface{})
	if !ok {
		t.Fatal("expected files array")
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestHandleDownload(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("admin", "pass")

	req := httptest.NewRequest("GET", "/download?path=admin/file.txt", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	downloadURL, ok := result["url"].(string)
	if !ok || downloadURL == "" {
		t.Fatal("expected non-empty URL")
	}
	if !strings.Contains(downloadURL, "/raw?") {
		t.Fatalf("expected proxy URL, got %s", downloadURL)
	}
}

func TestHandleGetContent(t *testing.T) {
	_, loginAs := setupTestApp(t)
	_ = loginAs("admin", "pass")

	adminUser, _ := model.GetUserByUsername("admin")
	key, err := auth.DeriveKey("0000000000000000000000000000000000000000000000000000000000000000", adminUser.ID)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	encrypted, err := auth.EncryptBytes(key, []byte("hello world!"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// We need to set a custom mock store on the handler
	// Since setupTestApp registers routes already, we need a different approach
	// Let's use a new app with custom mock
	testDB := setupTestDB(t)
	model.SetDB(testDB)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:             8080,
			SessionSecret:    "test-secret-key",
			EncryptionSecret: "0000000000000000000000000000000000000000000000000000000000000000",
		},
		Upload: config.UploadConfig{MaxFileSize: 10 * 1024 * 1024 * 1024},
	}

	sessions := model.NewSessionStore(testDB)
	challenges := auth.NewChallengeManager()
	auditWorker := model.NewAuditWorker(testDB)
	auditWorker.Start()
	mid := middleware.New(sessions, cfg.Server.SessionSecret)

	mock := &MockFileStore{
		GetObjectInfoFn: func(k string) (*store.FileInfo, error) {
			return &store.FileInfo{Name: "test.txt", Path: k, Size: int64(len(encrypted))}, nil
		},
		GetObjectContentFn: func(k string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(encrypted)), nil
		},
	}

	h := &Handler{
		Config: cfg, DB: testDB, Store: mock, Sessions: sessions,
		Audit: auditWorker, Challenges: challenges, Mid: mid,
	}

	app2 := fiber.New()
	h.RegisterRoutes(app2)

	user2, _ := model.CreateUser("admin2", "pass", "admin", model.PermAll)
	_ = user2
	sessionID, _ := sessions.Create(adminUser.ID, "admin", "admin", model.PermAll)
	cookie2 := auth.SignCookie(sessionID, cfg.Server.SessionSecret)

	req := httptest.NewRequest("GET", "/content?path=admin/test.txt", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie2})

	resp, _ := app2.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result2 map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result2)
	if result2["content"] != "hello world!" {
		t.Fatalf("expected 'hello world!', got %v", result2["content"])
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		header    string
		total     int64
		wantStart int64
		wantEnd   int64
		wantErr   bool
	}{
		{"bytes=0-499", 1000, 0, 499, false},
		{"bytes=500-", 1000, 500, 999, false},
		{"bytes=-100", 1000, 900, 999, false},
		{"bytes=0-0", 1000, 0, 0, false},
		{"bytes=999-999", 1000, 999, 999, false},
		{"bytes=0-9999", 1000, 0, 999, false},
		{"bytes=-2000", 1000, 0, 999, false},
		{"bytes=1000-2000", 1000, 0, 0, true},
		{"bytes=500-100", 1000, 0, 0, true},
		{"bytes=abc-def", 1000, 0, 0, true},
		{"bytes=0-100, 200-300", 1000, 0, 0, true},
		{"invalid", 1000, 0, 0, true},
	}

	for _, tt := range tests {
		start, end, err := parseRange(tt.header, tt.total)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseRange(%q, %d): expected error", tt.header, tt.total)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRange(%q, %d): unexpected error: %v", tt.header, tt.total, err)
			continue
		}
		if start != tt.wantStart || end != tt.wantEnd {
			t.Errorf("parseRange(%q, %d) = (%d, %d), want (%d, %d)", tt.header, tt.total, start, end, tt.wantStart, tt.wantEnd)
		}
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	app, _ := setupTestApp(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/auth/me"},
		{"GET", "/list"},
		{"POST", "/mkdir"},
		{"GET", "/admin/users"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 401 {
			t.Errorf("%s %s: expected 401, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}
