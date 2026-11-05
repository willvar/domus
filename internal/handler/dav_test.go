package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/oauth"
)

func TestDAVReportsDisabledOAuthWithAndWithoutSession(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	for _, session := range []struct {
		name   string
		cookie string
	}{
		{name: "anonymous"},
		{name: "signed_in", cookie: loginAs("alice", "password")},
	} {
		for _, target := range []string{"/dav", "/dav/"} {
			t.Run(session.name+target, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, target, nil)
				if session.cookie != "" {
					r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: session.cookie})
				}
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("disabled DAV status: got %d, want %d", w.Code, http.StatusServiceUnavailable)
				}
				var body struct {
					Error string `json:"error"`
				}
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body.Error != "oauth_not_configured" {
					t.Fatalf("disabled DAV error: got %q, decode error %v", body.Error, err)
				}
				if challenge := w.Header().Get("WWW-Authenticate"); challenge != "" {
					t.Fatalf("disabled DAV unexpectedly requested authentication: %q", challenge)
				}
			})
		}
	}
}

func TestDAVRejectsCookiesAndQueryTokensWhileAPIsRejectBearerTokens(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("alice", "password")
	h := &Handler{OAuth: &oauth.Server{}}
	for _, target := range []string{"/dav/", "/dav/?access_token=not-accepted", "/dav/?token=not-accepted"} {
		r := httptest.NewRequest("GET", target, nil)
		r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		w := httptest.NewRecorder()
		httpx.Handler(h.handleDAV).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer ") {
			t.Fatalf("cookie/query DAV %q: %d", target, w.Code)
		}
	}
	for _, target := range []string{"/user", "/audit/user/", "/file/access?path=/example.txt", "/ws"} {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Authorization", "Bearer files-read-token")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("Bearer authorized cookie API %q: %d", target, w.Code)
		}
	}
}
