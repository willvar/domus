package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/ws"
)

func TestNativeWebSocketHonorsConfiguredOriginsAndCookieAuth(t *testing.T) {
	sessions := model.NewMemRepos(nil).Sessions
	id, err := sessions.Create("alice", "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Config: &config.Config{Server: config.ServerConfig{CORSOrigins: "https://trusted-ui.example"}}, Hub: ws.NewHub()}
	defer h.Hub.CloseAll()
	mid := middleware.New(sessions, "test-secret")
	server := httptest.NewServer(httpx.ServerMiddleware(mid.AuthRequired(httpx.Handler(h.handleWebSocket)), h.Config.Server.CORSOrigins, nil))
	defer server.Close()
	for _, test := range []struct {
		origin string
		cookie bool
		status int
	}{
		{server.URL, true, 101}, {"https://trusted-ui.example", true, 101}, {"", true, 101},
		{"https://untrusted.example", true, 403}, {"https://trusted-ui.example", false, 401},
	} {
		header := http.Header{}
		if test.origin != "" {
			header.Set("Origin", test.origin)
		}
		if test.cookie {
			header.Set("Cookie", middleware.SessionCookieName+"="+auth.SignCookie(id, "test-secret"))
		}
		connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), header)
		if connection != nil {
			_ = connection.Close()
		}
		if response == nil || response.StatusCode != test.status {
			t.Fatalf("origin %q cookie %v: response %v, error %v", test.origin, test.cookie, response, err)
		}
		_ = response.Body.Close()
		if (err == nil) != (test.status == 101) {
			t.Fatalf("origin %q: %v", test.origin, err)
		}
	}
	header := http.Header{"Cookie": []string{middleware.LegacySessionCookieName + "=" + auth.SignCookie(id, "test-secret")}}
	connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	defer response.Body.Close()
	migrated := false
	for _, cookie := range response.Cookies() {
		if cookie.Name == middleware.SessionCookieName && cookie.Value == auth.SignCookie(id, "test-secret") && cookie.HttpOnly {
			migrated = true
		}
	}
	if !migrated {
		t.Fatal("upgrade lost the legacy cookie migration response header")
	}
}
