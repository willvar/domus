package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"domus/internal/middleware"
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
