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

	"encoding/hex"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/service"
)

func TestLogin_Success(t *testing.T) {
	app, repos, _ := setupTestApp(t)
	_, _ = repos.Users.Create("testuser", "testpass", "root", "")

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
	req2 := httptest.NewRequest("POST", "/auth", bytes.NewReader(loginBody))
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
	app, repos, _ := setupTestApp(t)
	_, _ = repos.Users.Create("testuser", "testpass", "root", "")

	body := `{"username":"testuser","password":"wrongpass"}`
	req := httptest.NewRequest("POST", "/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLogin_NonexistentUser(t *testing.T) {
	app, _, _ := setupTestApp(t)

	body := `{"username":"ghost","password":"pass"}`
	req := httptest.NewRequest("POST", "/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMe(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	req := httptest.NewRequest("GET", "/user/", nil)
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
	if result["username"] != "root" {
		t.Fatalf("expected admin, got %v", result["username"])
	}
	if _, exists := result["workspace_enabled"]; exists {
		t.Fatalf("workspace_enabled should not be exposed in the single-architecture API: %v", result["workspace_enabled"])
	}
}

func TestLogout(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	req := httptest.NewRequest("DELETE", "/auth", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	cleared := map[string]bool{}
	for _, responseCookie := range resp.Cookies() {
		if (responseCookie.Name == middleware.SessionCookieName || responseCookie.Name == middleware.LegacySessionCookieName) && responseCookie.MaxAge < 0 {
			cleared[responseCookie.Name] = true
		}
	}
	if !cleared[middleware.SessionCookieName] || !cleared[middleware.LegacySessionCookieName] {
		t.Fatalf("logout did not clear both session cookie names: %v", cleared)
	}

	req2 := httptest.NewRequest("GET", "/user/", nil)
	req2.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != 401 {
		t.Fatalf("expected 401 after logout, got %d", resp2.StatusCode)
	}
}

func TestListUsers_AsAdmin(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	req := httptest.NewRequest("GET", "/audit/user/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleList(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	adminUser, _ := repos.Users.GetByUsername("root")
	_ = repos.Files.Upsert(adminUser.ID, "/test.txt", "test.txt", false, 100, "", "")
	_ = repos.Files.Upsert(adminUser.ID, "/docs/", "docs", true, 0, "", "")
	_ = repos.Files.Upsert(adminUser.ID, "/.domus/", ".domus", true, 0, "", "")

	req := httptest.NewRequest("GET", "/file/?path=", nil)
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

	reserved := httptest.NewRequest("GET", "/file/?path=/.domus/", nil)
	reserved.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	reservedResponse, err := app.Test(reserved)
	if err != nil {
		t.Fatal(err)
	}
	if reservedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("reserved namespace path status = %d, want 403", reservedResponse.StatusCode)
	}
}

func TestHandleDownload(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	user, _ := repos.Users.GetByUsername("root")
	serverKey, _ := auth.ServerKeyFromSecret("0000000000000000000000000000000000000000000000000000000000000000")
	wrappedKEKBytes, _ := hex.DecodeString(user.WrappedKEK)
	kek, _ := auth.UnwrapKEK(serverKey, wrappedKEKBytes)
	dek, _ := auth.GenerateDEK()
	wrappedDEK, _ := auth.WrapDEK(kek, dek)

	ossPath := "/test.txt"
	_ = repos.Files.Upsert(user.ID, ossPath, "test.txt", false, 100, "text/plain", "abc123",
		model.UpsertFileOpts{WrappedDEK: hex.EncodeToString(wrappedDEK)})

	req := httptest.NewRequest("GET", "/file/access?path=/test.txt", nil)
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
	if !strings.Contains(downloadURL, "mock-oss.example.com") {
		t.Fatalf("expected mock oss URL, got %s", downloadURL)
	}
}

func TestPublicAvatarReturnsDirectEncryptedAccessDescriptor(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	_ = loginAs("root", "pass")

	user, err := repos.Users.GetByUsername("root")
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := auth.ServerKeyFromSecret("0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	wrappedKEK, err := hex.DecodeString(user.WrappedKEK)
	if err != nil {
		t.Fatal(err)
	}
	kek, err := auth.UnwrapKEK(serverKey, wrappedKEK)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := auth.GenerateDEK()
	if err != nil {
		t.Fatal(err)
	}
	wrappedDEK, err := auth.WrapDEK(kek, dek)
	if err != nil {
		t.Fatal(err)
	}
	avatarPath := "/.domus/user/avatar.webp"
	if err := repos.Files.Upsert(
		user.ID, avatarPath, "avatar.webp", false, 321, "image/webp", "",
		model.UpsertFileOpts{WrappedDEK: hex.EncodeToString(wrappedDEK), ObjectKey: "encrypted/avatar/object"},
	); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/user/avatar/root", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("avatar descriptor status = %d: %s", response.StatusCode, body)
	}
	if contentType := response.Header.Get(fiber.HeaderContentType); !strings.HasPrefix(contentType, fiber.MIMEApplicationJSON) {
		t.Fatalf("avatar descriptor content type = %q", contentType)
	}
	if cacheControl := response.Header.Get(fiber.HeaderCacheControl); cacheControl != "no-store" {
		t.Fatalf("avatar descriptor cache control = %q", cacheControl)
	}
	var descriptor struct {
		URL         string `json:"url"`
		DEK         string `json:"dek"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
		ChunkSize   int    `json:"chunk_size"`
	}
	if err := json.NewDecoder(response.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.URL != "https://mock-oss.example.com/encrypted/avatar/object?signed=true" ||
		descriptor.DEK != hex.EncodeToString(dek) || descriptor.Size != 321 ||
		descriptor.ContentType != "image/webp" || descriptor.ChunkSize != auth.DefaultChunkSize {
		t.Fatalf("unexpected avatar descriptor: %+v", descriptor)
	}
}

func TestRegisterRoutes_WithNilHub(t *testing.T) {
	repos := model.NewMemRepos(nil)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:             8080,
			SessionSecret:    "test-secret-key",
			EncryptionSecret: "0000000000000000000000000000000000000000000000000000000000000000",
		},
	}
	challenges := auth.NewChallengeManager()
	auditWorker := model.NewAuditWorker(nil)
	mid := middleware.New(repos.Sessions, cfg.Server.SessionSecret)

	h := &Handler{
		Config: cfg, Repos: repos, Store: &MockFileStore{},
		Email: &service.MockEmailSender{},
		Audit: auditWorker, Challenges: challenges, Mid: mid,
	}

	app := fiber.New()
	h.RegisterRoutes(app)

	req := httptest.NewRequest("GET", "/auth", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestParseRange removed -- parseRange was deleted as part of the SW decryption migration.
// Range parsing is now handled in the frontend Service Worker (sw.js).

func TestHandleMeExcludesEncryptionKey(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	req := httptest.NewRequest("GET", "/user/", nil)
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

	// KEK must NOT be returned to the frontend
	if _, exists := result["encryption_key"]; exists {
		t.Fatal("encryption_key must not be present in /user/ response")
	}
}

func TestHandleFileAccessReturnsDEK(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	user, _ := repos.Users.GetByUsername("root")

	// Derive the user's KEK and generate a wrapped DEK for the test file
	serverKey, _ := auth.ServerKeyFromSecret("0000000000000000000000000000000000000000000000000000000000000000")
	wrappedKEKBytes, _ := hex.DecodeString(user.WrappedKEK)
	kek, _ := auth.UnwrapKEK(serverKey, wrappedKEKBytes)
	dek, _ := auth.GenerateDEK()
	wrappedDEK, _ := auth.WrapDEK(kek, dek)

	ossPath := "/test.txt"
	_ = repos.Files.Upsert(user.ID, ossPath, "test.txt", false, 100, "text/plain", "abc123",
		model.UpsertFileOpts{WrappedDEK: hex.EncodeToString(wrappedDEK)})

	req := httptest.NewRequest("GET", "/file/access?path=/test.txt", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	if _, exists := result["wrapped_dek"]; exists {
		t.Fatal("response must not contain wrapped_dek")
	}
	dekHex, ok := result["dek"].(string)
	if !ok || dekHex == "" {
		t.Fatal("response missing dek field")
	}
	if len(dekHex) != 64 {
		t.Fatalf("expected dek to be 64 hex chars, got %d", len(dekHex))
	}

	returnedDEK, _ := hex.DecodeString(dekHex)
	if !bytes.Equal(returnedDEK, dek) {
		t.Fatal("returned DEK does not match original")
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	app, _, _ := setupTestApp(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/user/"},
		{"GET", "/file/"},
		{"POST", "/file/mkdir"},
		{"GET", "/audit/user/"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		resp, _ := app.Test(req)
		if resp.StatusCode != 401 {
			t.Errorf("%s %s: expected 401, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}
