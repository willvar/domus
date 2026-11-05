package oauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"domus/internal/auth"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
)

const testOrigin = "https://domus.example"
const testSecret = "test-only-session-signing-secret"
const verifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"

type oauthFixture struct {
	server                    *Server
	app                       http.Handler
	db                        *gorm.DB
	userID                    string
	alice, root, otherSession string
}

func setupOAuth(t *testing.T) *oauthFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres dbname=postgres sslmode=disable"
	}
	options, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*options)
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	schema := "domus_test_oauth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	options.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*options)
	pool.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	if err := db.Create(&model.User{ID: userID, Username: "alice", Role: "user", PasswordHash: "unused"}).Error; err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(db, testOrigin, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	sessions := model.NewMemRepos(nil).Sessions
	alice, err := sessions.Create(userID, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	root, err := sessions.Create("root-user", "root", "root")
	if err != nil {
		t.Fatal(err)
	}
	other, err := sessions.Create(userID, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux, middleware.New(sessions, testSecret))
	app := httpx.ServerMiddleware(mux, "", nil)
	return &oauthFixture{server: server, app: app, db: db, userID: userID, alice: alice, root: root, otherSession: other}
}

func (f *oauthFixture) request(method, target, contentType string, body io.Reader, session, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, body)
	r.Header.Set("Content-Type", contentType)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if session != "" {
		r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: auth.SignCookie(session, testSecret)})
	}
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	return w
}

func (f *oauthFixture) json(method, target string, body any, session string) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	return f.request(method, target, "application/json", bytes.NewReader(encoded), session, testOrigin)
}

type testClient struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret"`
	Public bool   `json:"public"`
}

func (f *oauthFixture) client(t *testing.T, public bool) testClient {
	t.Helper()
	w := f.json("POST", "/oauth/clients", map[string]any{"name": "Consumer app", "public": public, "redirect_uris": []string{"https://consumer.example/callback"}}, f.root)
	if w.Code != 201 {
		t.Fatalf("register %d: %s", w.Code, w.Body)
	}
	var client testClient
	if err := json.Unmarshal(w.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}
	return client
}

func authorizationQuery(client testClient) url.Values {
	hash := sha256.Sum256([]byte(verifier))
	return url.Values{"client_id": {client.ID}, "redirect_uri": {"https://consumer.example/callback"},
		"response_type": {"code"}, "scope": {ReadScope}, "state": {"random-state-not-a-session"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
}

func (f *oauthFixture) challenge(t *testing.T, query url.Values) string {
	t.Helper()
	w := f.json("POST", "/oauth/consent", map[string]string{"query": query.Encode()}, f.alice)
	if w.Code != 200 {
		t.Fatalf("challenge %d: %s", w.Code, w.Body)
	}
	var result struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Challenge == "" {
		t.Fatalf("challenge response %s: %v", w.Body, err)
	}
	return result.Challenge
}

func (f *oauthFixture) decision(challenge, decision, session string) *httptest.ResponseRecorder {
	values := url.Values{"challenge": {challenge}, "decision": {decision}}
	return f.request("POST", "/oauth/decision", "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), session, testOrigin)
}

func (f *oauthFixture) code(t *testing.T, client testClient) string {
	t.Helper()
	challenge := f.challenge(t, authorizationQuery(client))
	w := f.decision(challenge, "allow", f.alice)
	location, err := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || err != nil || location.Query().Get("code") == "" || location.Query().Get("state") != "random-state-not-a-session" {
		t.Fatalf("approve %d %v: %s", w.Code, w.Header(), w.Body)
	}
	return location.Query().Get("code")
}

func (f *oauthFixture) token(client testClient, values url.Values) *httptest.ResponseRecorder {
	values.Set("client_id", client.ID)
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !client.Public {
		r.SetBasicAuth(client.ID, client.Secret)
	}
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	return w
}

type tokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Scope   string `json:"scope"`
	Type    string `json:"token_type"`
}

func decodeTokens(t *testing.T, w *httptest.ResponseRecorder) tokens {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("token status %d: %s", w.Code, w.Body)
	}
	var result tokens
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Access == "" || result.Refresh == "" || result.Type != "bearer" || result.Scope != ReadScope {
		t.Fatalf("missing token fields")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token response may be cached")
	}
	return result
}

func (f *oauthFixture) exchange(client testClient, code, proof string) *httptest.ResponseRecorder {
	return f.token(client, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://consumer.example/callback"}, "code_verifier": {proof}})
}

func (f *oauthFixture) authenticate(token string) bool {
	r := httptest.NewRequest("GET", "/dav/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	id, err := f.server.Authenticate(r)
	return err == nil && id == f.userID
}

func TestOAuthConsentPKCEPersistenceAndRevocation(t *testing.T) {
	f := setupOAuth(t)
	client := f.client(t, false)
	query := authorizationQuery(client)
	w := f.request("GET", "/oauth/authorize?"+query.Encode(), "", nil, "", "")
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), testOrigin+"/authorize?") {
		t.Fatalf("authorize %d %v", w.Code, w.Header())
	}
	challenge := f.challenge(t, query)
	if w := f.decision(challenge, "allow", f.otherSession); w.Code != 400 {
		t.Fatalf("other session consent %d", w.Code)
	}
	if w := f.decision(challenge, "allow", f.root); w.Code != 400 {
		t.Fatalf("other user consent %d", w.Code)
	}
	w = f.decision(challenge, "deny", f.alice)
	location, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || location.Query().Get("error") != "access_denied" {
		t.Fatalf("deny %d %s", w.Code, w.Body)
	}
	if w := f.decision(challenge, "allow", f.alice); w.Code != 400 {
		t.Fatalf("consent reused %d", w.Code)
	}
	code := f.code(t, client)
	if w := f.exchange(client, code, ""); w.Code == 200 {
		t.Fatal("missing PKCE verifier accepted")
	}
	if w := f.exchange(client, code, strings.Repeat("x", 64)); w.Code == 200 {
		t.Fatal("invalid PKCE accepted")
	}
	issued := decodeTokens(t, f.exchange(client, code, verifier))
	if !f.authenticate(issued.Access) || f.authenticate(issued.Refresh) {
		t.Fatal("access/refresh token boundary failed")
	}
	newServer, err := NewServer(f.db, testOrigin, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	f.server = newServer
	if !f.authenticate(issued.Access) {
		t.Fatal("tokens did not survive reopening the persistent store")
	}
	var rows []credential
	if err := f.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(row.Payload, issued.Access) || strings.Contains(row.Payload, issued.Refresh) || strings.Contains(row.Payload, client.Secret) || strings.Contains(row.Payload, testSecret) || strings.Contains(row.Payload, verifier) {
			t.Fatal("credential snapshot contains raw secret material")
		}
	}
	w = f.request("GET", "/oauth/grants", "", nil, f.alice, "")
	var grants []Grant
	if err := json.Unmarshal(w.Body.Bytes(), &grants); err != nil || len(grants) != 1 {
		t.Fatalf("grants %s: %v", w.Body, err)
	}
	if w := f.json("DELETE", "/oauth/grants/"+grants[0].ID, nil, f.root); w.Code != 404 {
		t.Fatalf("cross-user revoke %d", w.Code)
	}
	if w := f.json("DELETE", "/oauth/grants/"+grants[0].ID, nil, f.alice); w.Code != 200 {
		t.Fatalf("revoke %d: %s", w.Code, w.Body)
	}
	if f.authenticate(issued.Access) {
		t.Fatal("revoked grant still authorized")
	}
	if w := f.token(client, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {issued.Refresh}}); w.Code == 200 {
		t.Fatal("revoked grant refreshed")
	}
}

func TestOAuthRotationReplayAndConcurrentConsumption(t *testing.T) {
	f := setupOAuth(t)
	client := f.client(t, true)
	t.Run("rotation and reuse revoke family", func(t *testing.T) {
		first := decodeTokens(t, f.exchange(client, f.code(t, client), verifier))
		second := decodeTokens(t, f.token(client, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.Refresh}}))
		if first.Refresh == second.Refresh || f.authenticate(first.Access) || !f.authenticate(second.Access) {
			t.Fatal("refresh did not rotate and replace the access token")
		}
		if w := f.token(client, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.Refresh}}); w.Code == 200 {
			t.Fatal("refresh reuse accepted")
		}
		if f.authenticate(second.Access) {
			t.Fatal("reused refresh token did not revoke the live family")
		}
	})
	t.Run("authorization code is single-use under concurrency", func(t *testing.T) {
		code := f.code(t, client)
		results := make([]*httptest.ResponseRecorder, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() { results[i] = f.exchange(client, code, verifier) })
		}
		wg.Wait()
		successes := 0
		for _, w := range results {
			if w.Code == 200 {
				successes++
				issued := decodeTokens(t, w)
				if f.authenticate(issued.Access) {
					t.Fatal("replayed code did not revoke family")
				}
			} else if w.Code != 400 {
				t.Fatalf("concurrent code response %d: %s", w.Code, w.Body)
			}
		}
		if successes != 1 {
			t.Fatalf("successful concurrent code exchanges: %d", successes)
		}
	})
	t.Run("refresh token is single-use under concurrency", func(t *testing.T) {
		first := decodeTokens(t, f.exchange(client, f.code(t, client), verifier))
		results := make([]*httptest.ResponseRecorder, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() {
				results[i] = f.token(client, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.Refresh}})
			})
		}
		wg.Wait()
		successes := 0
		for _, w := range results {
			if w.Code == 200 {
				successes++
				issued := decodeTokens(t, w)
				if f.authenticate(issued.Access) {
					t.Fatal("concurrent refresh replay did not revoke family")
				}
			} else if w.Code != 400 {
				t.Fatalf("concurrent refresh response %d: %s", w.Code, w.Body)
			}
		}
		if successes != 1 {
			t.Fatalf("successful concurrent refreshes: %d", successes)
		}
	})
}

func TestOAuthStandardRevocationAndExpiration(t *testing.T) {
	f := setupOAuth(t)
	client := f.client(t, true)
	other := f.client(t, true)
	issued := decodeTokens(t, f.exchange(client, f.code(t, client), verifier))
	revoke := func(client testClient, token string) *httptest.ResponseRecorder {
		values := url.Values{"client_id": {client.ID}, "token": {token}, "token_type_hint": {"refresh_token"}}
		return f.request("POST", "/oauth/revoke", "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), "", "")
	}
	if w := revoke(other, issued.Refresh); w.Code != 200 {
		t.Fatalf("unrelated client revocation status %d", w.Code)
	}
	if !f.authenticate(issued.Access) {
		t.Fatal("unrelated client revoked authorization")
	}
	if w := revoke(client, "invalid-token"); w.Code != 200 {
		t.Fatalf("invalid token revocation status %d", w.Code)
	}
	if w := revoke(client, issued.Refresh); w.Code != 200 {
		t.Fatalf("revocation status %d", w.Code)
	}
	if f.authenticate(issued.Access) {
		t.Fatal("standard revocation did not revoke family")
	}
	issued = decodeTokens(t, f.exchange(client, f.code(t, client), verifier))
	if err := f.db.Model(&credential{}).Where("kind = ? AND active = ?", "access", true).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if f.authenticate(issued.Access) {
		t.Fatal("expired access token accepted")
	}
	code := f.code(t, client)
	if err := f.db.Model(&credential{}).Where("kind = ? AND active = ?", "code", true).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.exchange(client, code, verifier); w.Code == 200 {
		t.Fatal("expired authorization code accepted")
	}
	if err := f.db.Model(&credential{}).Where("kind = ? AND active = ?", "refresh", true).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.token(client, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {issued.Refresh}}); w.Code == 200 {
		t.Fatal("expired refresh token accepted")
	}
}

func TestOAuthValidationClientDisableAndDeletedUser(t *testing.T) {
	f := setupOAuth(t)
	client := f.client(t, true)
	for _, mutate := range []func(url.Values){
		func(v url.Values) { v.Del("code_challenge") },
		func(v url.Values) { v.Set("code_challenge_method", "plain") },
		func(v url.Values) { v.Set("scope", "files:write") },
		func(v url.Values) { v.Set("redirect_uri", "https://unregistered.example/callback") },
		func(v url.Values) { v.Add("scope", ReadScope) },
		func(v url.Values) { v.Set("response_type", "token") },
		func(v url.Values) { v.Set("response_mode", "form_post") },
		func(v url.Values) { v.Set("request_uri", "https://untrusted.example/request") },
	} {
		query := authorizationQuery(client)
		mutate(query)
		w := f.json("POST", "/oauth/consent", map[string]string{"query": query.Encode()}, f.alice)
		if w.Code != 400 {
			t.Fatalf("invalid consent %d: %s", w.Code, w.Body)
		}
		// Invalid public requests must not panic or redirect to the consent UI.
		w = f.request("GET", "/oauth/authorize?"+query.Encode(), "", nil, "", "")
		if w.Code == 500 || strings.HasPrefix(w.Header().Get("Location"), testOrigin+"/authorize?") {
			t.Fatal("invalid authorize request accepted")
		}
	}
	w := f.request("POST", "/oauth/clients", "application/json", strings.NewReader(`{"name":"CSRF"}`), f.root, "https://untrusted.example")
	if w.Code != 403 {
		t.Fatalf("cross-origin change %d", w.Code)
	}
	if w := f.json("POST", "/oauth/clients", map[string]any{"name": "No privilege", "public": true, "redirect_uris": []string{"https://consumer.example/callback"}}, f.alice); w.Code != 403 {
		t.Fatalf("non-admin registration %d", w.Code)
	}
	for _, grantType := range []string{"password", "client_credentials"} {
		if w := f.token(client, url.Values{"grant_type": {grantType}}); w.Code == 200 {
			t.Fatalf("%s enabled", grantType)
		}
	}
	challenge := f.challenge(t, authorizationQuery(client))
	if err := f.db.Model(&consent{}).Where("id = ?", challenge).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.decision(challenge, "allow", f.alice); w.Code != 400 {
		t.Fatal("expired consent accepted")
	}
	issued := decodeTokens(t, f.exchange(client, f.code(t, client), verifier))
	if w := f.json("DELETE", "/oauth/clients/"+client.ID, nil, f.root); w.Code != 200 {
		t.Fatalf("disable %d: %s", w.Code, w.Body)
	}
	if f.authenticate(issued.Access) {
		t.Fatal("disabled client still authorized")
	}
	client = f.client(t, true)
	issued = decodeTokens(t, f.exchange(client, f.code(t, client), verifier))
	if err := f.db.Where("id = ?", f.userID).Delete(&model.User{}).Error; err != nil {
		t.Fatal(err)
	}
	if f.authenticate(issued.Access) {
		t.Fatal("deleted user still authorized")
	}
}
