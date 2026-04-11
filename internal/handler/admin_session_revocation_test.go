package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/ws"
)

func TestAdminUpdateUserRoleChangeRevokesSessionsHTTP(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)

	adminCookie := loginAs("root", "pass")
	victimCookie := loginAs("victim-role-http", "pass")
	victim, _ := repos.Users.GetByUsername("victim-role-http")

	body, _ := json.Marshal(map[string]string{"role": "user"})
	req := httptest.NewRequest("PUT", "/audit/user/"+victim.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: adminCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	meReq := httptest.NewRequest("GET", "/user/", nil)
	meReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: victimCookie})
	meResp, err := app.Test(meReq)
	if err != nil {
		t.Fatal(err)
	}
	if meResp.StatusCode != 401 {
		t.Fatalf("expected victim session to be revoked, got %d", meResp.StatusCode)
	}

	updated, err := repos.Users.GetByID(victim.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != "user" {
		t.Fatalf("expected role user, got %s", updated.Role)
	}
}

func TestAdminUpdateUserRejectsInvalidRoleHTTP(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)

	adminCookie := loginAs("root", "pass")
	victimCookie := loginAs("victim-invalid-role-http", "pass")
	victim, _ := repos.Users.GetByUsername("victim-invalid-role-http")
	originalRole := victim.Role

	body, _ := json.Marshal(map[string]string{"role": "super-admin"})
	req := httptest.NewRequest("PUT", "/audit/user/"+victim.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: adminCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "invalid_role" {
		t.Fatalf("expected invalid_role, got %v", result["error"])
	}

	meReq := httptest.NewRequest("GET", "/user/", nil)
	meReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: victimCookie})
	meResp, err := app.Test(meReq)
	if err != nil {
		t.Fatal(err)
	}
	if meResp.StatusCode != 200 {
		t.Fatalf("expected victim session to stay valid, got %d", meResp.StatusCode)
	}

	updated, err := repos.Users.GetByID(victim.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != originalRole {
		t.Fatalf("expected role unchanged (%s), got %s", originalRole, updated.Role)
	}
}

func TestAdminUpdateUserRejectsDemotingLastRootHTTP(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)

	rootCookie := loginAs("root", "pass")
	rootUser, _ := repos.Users.GetByUsername("root")

	body, _ := json.Marshal(map[string]string{"role": "user"})
	req := httptest.NewRequest("PUT", "/audit/user/"+rootUser.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: rootCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "cannot_demote_last_root" {
		t.Fatalf("expected cannot_demote_last_root, got %v", result["error"])
	}

	meReq := httptest.NewRequest("GET", "/user/", nil)
	meReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: rootCookie})
	meResp, err := app.Test(meReq)
	if err != nil {
		t.Fatal(err)
	}
	if meResp.StatusCode != 200 {
		t.Fatalf("expected root session to stay valid, got %d", meResp.StatusCode)
	}

	updated, err := repos.Users.GetByID(rootUser.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != "root" {
		t.Fatalf("expected role root, got %s", updated.Role)
	}
}

func TestAdminResetUserOTPRevokesSessionsHTTP(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)

	adminCookie := loginAs("root", "pass")
	victimCookie := loginAs("victim-otp-http", "pass")
	victim, _ := repos.Users.GetByUsername("victim-otp-http")
	if err := repos.Users.UpdateTOTP(victim.ID, "otp-secret", true); err != nil {
		t.Fatalf("seed otp: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/audit/user/"+victim.ID+"/otp", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: adminCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	meReq := httptest.NewRequest("GET", "/user/", nil)
	meReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: victimCookie})
	meResp, err := app.Test(meReq)
	if err != nil {
		t.Fatal(err)
	}
	if meResp.StatusCode != 401 {
		t.Fatalf("expected victim session to be revoked, got %d", meResp.StatusCode)
	}

	updated, err := repos.Users.GetByID(victim.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.TOTPEnabled {
		t.Fatal("expected totp to be disabled")
	}
	if updated.TOTPSecret != "" {
		t.Fatalf("expected empty totp secret, got %q", updated.TOTPSecret)
	}
}

func TestAdminResetUserEmailRevokesSessionsHTTP(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)

	adminCookie := loginAs("root", "pass")
	victimCookie := loginAs("victim-email-http", "pass")
	victim, _ := repos.Users.GetByUsername("victim-email-http")
	if err := repos.Users.UpdateEmail(victim.ID, "victim@example.com"); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/audit/user/"+victim.ID+"/email", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: adminCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	meReq := httptest.NewRequest("GET", "/user/", nil)
	meReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: victimCookie})
	meResp, err := app.Test(meReq)
	if err != nil {
		t.Fatal(err)
	}
	if meResp.StatusCode != 401 {
		t.Fatalf("expected victim session to be revoked, got %d", meResp.StatusCode)
	}

	updated, err := repos.Users.GetByID(victim.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Email != "" {
		t.Fatalf("expected empty email, got %q", updated.Email)
	}
}

func TestAdminUpdateUserRoleChangeRevokesSessionsWS(t *testing.T) {
	repos := model.NewMemRepos(nil)

	audit := model.NewAuditWorker(nil)
	h := &Handler{
		Repos: repos,
		Audit: audit,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{},
	}

	admin, _ := repos.Users.Create("admin-role-ws", "pass", "root", "")
	victim, _ := repos.Users.Create("victim-role-ws", "pass", "user", "")
	victimSessionID, err := repos.Sessions.Create(victim.ID, victim.Username, victim.Role)
	if err != nil {
		t.Fatalf("create victim session: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"id": victim.ID, "role": "root"})
	conn := &ws.Conn{
		Session: &model.Session{UserID: admin.ID, Username: admin.Username, Role: "root"},
	}
	result, err := h.wsAdminUpdateUser(conn, "", payload)
	if err != nil {
		t.Fatalf("wsAdminUpdateUser: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["ok"] != true {
		t.Fatalf("expected ok response, got %#v", result)
	}
	if repos.Sessions.Get(victimSessionID) != nil {
		t.Fatal("expected victim session to be revoked")
	}
}

func TestAdminUpdateUserRejectsInvalidRoleWS(t *testing.T) {
	repos := model.NewMemRepos(nil)

	audit := model.NewAuditWorker(nil)
	h := &Handler{
		Repos: repos,
		Audit: audit,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{},
	}

	admin, _ := repos.Users.Create("admin-invalid-role-ws", "pass", "root", "")
	victim, _ := repos.Users.Create("victim-invalid-role-ws", "pass", "user", "")
	victimSessionID, err := repos.Sessions.Create(victim.ID, victim.Username, victim.Role)
	if err != nil {
		t.Fatalf("create victim session: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"id": victim.ID, "role": "invalid-role"})
	conn := &ws.Conn{
		Session: &model.Session{UserID: admin.ID, Username: admin.Username, Role: "root"},
	}
	_, err = h.wsAdminUpdateUser(conn, "", payload)
	if err == nil {
		t.Fatal("expected invalid_role error")
	}
	wsErr, ok := err.(*wsError)
	if !ok {
		t.Fatalf("expected wsError, got %T", err)
	}
	if wsErr.Code != "invalid_role" {
		t.Fatalf("expected invalid_role, got %s", wsErr.Code)
	}
	if repos.Sessions.Get(victimSessionID) == nil {
		t.Fatal("expected victim session to remain valid")
	}

	updated, err := repos.Users.GetByID(victim.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != "user" {
		t.Fatalf("expected role unchanged user, got %s", updated.Role)
	}
}

func TestAdminUpdateUserRejectsDemotingLastRootWS(t *testing.T) {
	repos := model.NewMemRepos(nil)

	audit := model.NewAuditWorker(nil)
	h := &Handler{
		Repos: repos,
		Audit: audit,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{},
	}

	root, _ := repos.Users.Create("root-last-ws", "pass", "root", "")
	rootSessionID, err := repos.Sessions.Create(root.ID, root.Username, root.Role)
	if err != nil {
		t.Fatalf("create root session: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"id": root.ID, "role": "user"})
	conn := &ws.Conn{
		Session: &model.Session{UserID: root.ID, Username: root.Username, Role: "root"},
	}
	_, err = h.wsAdminUpdateUser(conn, "", payload)
	if err == nil {
		t.Fatal("expected cannot_demote_last_root error")
	}
	wsErr, ok := err.(*wsError)
	if !ok {
		t.Fatalf("expected wsError, got %T", err)
	}
	if wsErr.Code != "cannot_demote_last_root" {
		t.Fatalf("expected cannot_demote_last_root, got %s", wsErr.Code)
	}
	if repos.Sessions.Get(rootSessionID) == nil {
		t.Fatal("expected root session to remain valid")
	}

	updated, err := repos.Users.GetByID(root.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != "root" {
		t.Fatalf("expected role root, got %s", updated.Role)
	}
}

func TestAdminResetUserOTPRevokesSessionsWS(t *testing.T) {
	repos := model.NewMemRepos(nil)

	audit := model.NewAuditWorker(nil)
	h := &Handler{
		Repos: repos,
		Audit: audit,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{},
	}

	admin, _ := repos.Users.Create("admin-otp-ws", "pass", "root", "")
	victim, _ := repos.Users.Create("victim-otp-ws", "pass", "user", "")
	_ = repos.Users.UpdateTOTP(victim.ID, "otp-secret", true)
	victimSessionID, err := repos.Sessions.Create(victim.ID, victim.Username, victim.Role)
	if err != nil {
		t.Fatalf("create victim session: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"id": victim.ID})
	conn := &ws.Conn{
		Session: &model.Session{UserID: admin.ID, Username: admin.Username, Role: "root"},
	}
	result, err := h.wsAdminResetUserOTP(conn, "", payload)
	if err != nil {
		t.Fatalf("wsAdminResetUserOTP: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["ok"] != true {
		t.Fatalf("expected ok response, got %#v", result)
	}
	if repos.Sessions.Get(victimSessionID) != nil {
		t.Fatal("expected victim session to be revoked")
	}
}

func TestAdminResetUserEmailRevokesSessionsWS(t *testing.T) {
	repos := model.NewMemRepos(nil)

	audit := model.NewAuditWorker(nil)
	h := &Handler{
		Repos: repos,
		Audit: audit,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{},
	}

	admin, _ := repos.Users.Create("admin-email-ws", "pass", "root", "")
	victim, _ := repos.Users.Create("victim-email-ws", "pass", "user", "")
	_ = repos.Users.UpdateEmail(victim.ID, "victim@example.com")
	victimSessionID, err := repos.Sessions.Create(victim.ID, victim.Username, victim.Role)
	if err != nil {
		t.Fatalf("create victim session: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"id": victim.ID})
	conn := &ws.Conn{
		Session: &model.Session{UserID: admin.ID, Username: admin.Username, Role: "root"},
	}
	result, err := h.wsAdminResetUserEmail(conn, "", payload)
	if err != nil {
		t.Fatalf("wsAdminResetUserEmail: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["ok"] != true {
		t.Fatalf("expected ok response, got %#v", result)
	}
	if repos.Sessions.Get(victimSessionID) != nil {
		t.Fatal("expected victim session to be revoked")
	}
}
