package handler

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/model"
)

const testServerEncryptionSecret = "0000000000000000000000000000000000000000000000000000000000000000"

func mustCreateReadyEncryptedFile(t *testing.T, repos *model.Repos, username, appPath, name string, size int64) {
	t.Helper()

	user, err := repos.Users.GetByUsername(username)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	serverKey, err := auth.ServerKeyFromSecret(testServerEncryptionSecret)
	if err != nil {
		t.Fatalf("server key: %v", err)
	}
	wrappedKEKBytes, err := hex.DecodeString(user.WrappedKEK)
	if err != nil {
		t.Fatalf("decode wrapped kek: %v", err)
	}
	kek, err := auth.UnwrapKEK(serverKey, wrappedKEKBytes)
	if err != nil {
		t.Fatalf("unwrap kek: %v", err)
	}
	dek, err := auth.GenerateDEK()
	if err != nil {
		t.Fatalf("generate dek: %v", err)
	}
	wrappedDEK, err := auth.WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("wrap dek: %v", err)
	}

	ossPath := username + appPath
	if err := repos.Files.Upsert(user.ID, ossPath, name, false, size, "text/plain", "hash",
		model.UpsertFileOpts{WrappedDEK: hex.EncodeToString(wrappedDEK)}); err != nil {
		t.Fatalf("upsert file: %v", err)
	}
}

func createShareRequest(t *testing.T, app *fiber.App, cookie string, payload map[string]any) *http.Response {
	t.Helper()

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/file/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustUserKEK(t *testing.T, repos *model.Repos, username string) []byte {
	t.Helper()
	user, err := repos.Users.GetByUsername(username)
	if err != nil {
		t.Fatalf("get user %s: %v", username, err)
	}
	serverKey, err := auth.ServerKeyFromSecret(testServerEncryptionSecret)
	if err != nil {
		t.Fatalf("server key: %v", err)
	}
	wrappedKEKBytes, err := hex.DecodeString(user.WrappedKEK)
	if err != nil {
		t.Fatalf("decode wrapped kek: %v", err)
	}
	kek, err := auth.UnwrapKEK(serverKey, wrappedKEKBytes)
	if err != nil {
		t.Fatalf("unwrap kek: %v", err)
	}
	return kek
}

func TestCreateShareRejectsDirectory(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	user, _ := repos.Users.GetByUsername("root")
	_ = repos.Files.Upsert(user.ID, "root/home/root/docs/", "docs", true, 0, "", "")

	resp := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/docs/",
		"target_username": "alice",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "directory_not_shareable" {
		t.Fatalf("expected directory_not_shareable, got %v", result["error"])
	}
}

func TestCreateShareRejectsUnencryptedFile(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	user, _ := repos.Users.GetByUsername("root")
	_ = repos.Files.Upsert(user.ID, "root/home/root/plain.txt", "plain.txt", false, 1, "text/plain", "hash")

	resp := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/plain.txt",
		"target_username": "alice",
	})
	if resp.StatusCode != 409 {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "file_not_encrypted" {
		t.Fatalf("expected file_not_encrypted, got %v", result["error"])
	}
}

func TestCreateShareRejectsSelfTarget(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	mustCreateReadyEncryptedFile(t, repos, "root", "/home/root/s2.txt", "s2.txt", 1)

	respSelf := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/s2.txt",
		"target_username": "root",
	})
	if respSelf.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", respSelf.StatusCode)
	}
	var selfResult map[string]any
	_ = json.NewDecoder(respSelf.Body).Decode(&selfResult)
	if selfResult["error"] != "cannot_share_with_self" {
		t.Fatalf("expected cannot_share_with_self, got %v", selfResult["error"])
	}
}

func TestCreateShareRejectsInvalidExpiry(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	mustCreateReadyEncryptedFile(t, repos, "root", "/home/root/exp.txt", "exp.txt", 1)

	resp := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/exp.txt",
		"target_username": "alice",
		"expires_in":      maxShareExpirySeconds + 1,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "invalid_expiry" {
		t.Fatalf("expected invalid_expiry, got %v", result["error"])
	}
}

func TestUserShareWithExpiry(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	mustCreateReadyEncryptedFile(t, repos, "root", "/home/root/expiry.txt", "expiry.txt", 1)

	resp := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/expiry.txt",
		"target_username": "alice",
		"expires_in":      3600,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	shareID, _ := result["share_id"].(string)
	if shareID == "" {
		t.Fatal("expected share_id")
	}

	share, err := repos.Shares.GetByID(shareID)
	if err != nil {
		t.Fatalf("get share: %v", err)
	}
	if share.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	if share.ExpiresAt.Before(time.Now()) {
		t.Fatal("expected ExpiresAt to be in the future")
	}
}

func TestUserShareExpired(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	_ = loginAs("root", "pass")
	aliceCookie := loginAs("alice", "pass")

	owner, _ := repos.Users.GetByUsername("root")
	alice, _ := repos.Users.GetByUsername("alice")
	aliceKEK := mustUserKEK(t, repos, "alice")
	dek, _ := auth.GenerateDEK()
	wrappedForAlice, _ := auth.WrapDEK(aliceKEK, dek)

	exp := time.Now().Add(-time.Minute)
	if err := repos.Shares.Create(&model.Share{
		ShareID:      "expired-user-share",
		OwnerID:      owner.ID,
		FilePath:     "root/home/root/e.txt",
		FileName:     "e.txt",
		FileSize:     1,
		ContentType:  "text/plain",
		TargetUserID: alice.ID,
		WrappedDEK:   hex.EncodeToString(wrappedForAlice),
		Permission:   "read",
		ExpiresAt:    &exp,
	}); err != nil {
		t.Fatalf("create share: %v", err)
	}

	req := httptest.NewRequest("GET", "/file/shared/expired-user-share", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: aliceCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 410 {
		t.Fatalf("expected 410, got %d", resp.StatusCode)
	}
}

func TestShareInfoUserShareAuthBoundaries(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	_ = loginAs("root", "pass")
	aliceCookie := loginAs("alice", "pass")
	bobCookie := loginAs("bob", "pass")

	owner, _ := repos.Users.GetByUsername("root")
	alice, _ := repos.Users.GetByUsername("alice")
	aliceKEK := mustUserKEK(t, repos, "alice")
	dek, err := auth.GenerateDEK()
	if err != nil {
		t.Fatalf("generate dek: %v", err)
	}
	wrappedForAlice, err := auth.WrapDEK(aliceKEK, dek)
	if err != nil {
		t.Fatalf("wrap dek: %v", err)
	}
	if err := repos.Shares.Create(&model.Share{
		ShareID:      "user-share-auth",
		OwnerID:      owner.ID,
		FilePath:     "root/home/root/u.txt",
		FileName:     "u.txt",
		FileSize:     2,
		ContentType:  "text/plain",
		TargetUserID: alice.ID,
		WrappedDEK:   hex.EncodeToString(wrappedForAlice),
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create share: %v", err)
	}

	// No auth -> 401 (middleware blocks)
	reqNoAuth := httptest.NewRequest("GET", "/file/shared/user-share-auth", nil)
	respNoAuth, err := app.Test(reqNoAuth)
	if err != nil {
		t.Fatal(err)
	}
	if respNoAuth.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", respNoAuth.StatusCode)
	}

	// Wrong user -> 403
	reqOther := httptest.NewRequest("GET", "/file/shared/user-share-auth", nil)
	reqOther.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: bobCookie})
	respOther, err := app.Test(reqOther)
	if err != nil {
		t.Fatal(err)
	}
	if respOther.StatusCode != 403 {
		t.Fatalf("expected 403, got %d", respOther.StatusCode)
	}

	// Target user -> 200 with dek
	reqTarget := httptest.NewRequest("GET", "/file/shared/user-share-auth", nil)
	reqTarget.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: aliceCookie})
	respTarget, err := app.Test(reqTarget)
	if err != nil {
		t.Fatal(err)
	}
	if respTarget.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", respTarget.StatusCode)
	}
	var targetResult map[string]any
	_ = json.NewDecoder(respTarget.Body).Decode(&targetResult)
	dekHex, _ := targetResult["dek"].(string)
	if len(dekHex) != 64 {
		t.Fatalf("expected 64-char dek, got %q", dekHex)
	}
	returnedDEK, err := hex.DecodeString(dekHex)
	if err != nil {
		t.Fatalf("decode returned dek: %v", err)
	}
	if !bytes.Equal(returnedDEK, dek) {
		t.Fatal("returned dek does not match original")
	}
	if _, ok := targetResult["wrapped_dek"]; ok {
		t.Fatal("did not expect wrapped_dek for user share")
	}
}

func TestDeleteShareReturns404ForUnrelatedUser(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	_ = loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	bobCookie := loginAs("bob", "pass")

	owner, _ := repos.Users.GetByUsername("root")
	alice, _ := repos.Users.GetByUsername("alice")
	if err := repos.Shares.Create(&model.Share{
		ShareID:      "delete-unrelated-check",
		OwnerID:      owner.ID,
		FilePath:     "root/home/root/del.txt",
		FileName:     "del.txt",
		FileSize:     1,
		ContentType:  "text/plain",
		TargetUserID: alice.ID,
		WrappedDEK:   "aa",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create share: %v", err)
	}
	share, err := repos.Shares.GetByID("delete-unrelated-check")
	if err != nil {
		t.Fatalf("get share: %v", err)
	}

	// Bob is neither owner nor target -- should get 404
	req := httptest.NewRequest("DELETE", "/file/share/"+strconv.FormatInt(share.ID, 10), nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: bobCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestTargetUserCanDeleteShare(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	_ = loginAs("root", "pass")
	aliceCookie := loginAs("alice", "pass")

	owner, _ := repos.Users.GetByUsername("root")
	alice, _ := repos.Users.GetByUsername("alice")
	if err := repos.Shares.Create(&model.Share{
		ShareID:      "target-can-delete",
		OwnerID:      owner.ID,
		FilePath:     "root/home/root/del2.txt",
		FileName:     "del2.txt",
		FileSize:     1,
		ContentType:  "text/plain",
		TargetUserID: alice.ID,
		WrappedDEK:   "aa",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create share: %v", err)
	}
	share, err := repos.Shares.GetByID("target-can-delete")
	if err != nil {
		t.Fatalf("get share: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/file/share/"+strconv.FormatInt(share.ID, 10), nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: aliceCookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	_, err = repos.Shares.GetByID("target-can-delete")
	if err == nil {
		t.Fatal("expected share to be deleted")
	}
}

func TestDeleteFileInvalidatesUserShare(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	mustCreateReadyEncryptedFile(t, repos, "root", "/home/root/will-delete.txt", "will-delete.txt", 1)

	shareResp := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/will-delete.txt",
		"target_username": "alice",
	})
	if shareResp.StatusCode != 200 {
		t.Fatalf("create share: expected 200, got %d", shareResp.StatusCode)
	}
	var shareResult map[string]any
	_ = json.NewDecoder(shareResp.Body).Decode(&shareResult)
	shareID, _ := shareResult["share_id"].(string)
	if shareID == "" {
		t.Fatal("expected share_id")
	}

	delReq := httptest.NewRequest("DELETE", "/file/delete?path=/home/root/will-delete.txt", nil)
	delReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	delResp, err := app.Test(delReq)
	if err != nil {
		t.Fatal(err)
	}
	if delResp.StatusCode != 200 {
		t.Fatalf("delete file: expected 200, got %d", delResp.StatusCode)
	}

	aliceCookie := loginAs("alice", "pass")
	infoReq := httptest.NewRequest("GET", "/file/shared/"+shareID, nil)
	infoReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: aliceCookie})
	infoResp, err := app.Test(infoReq)
	if err != nil {
		t.Fatal(err)
	}
	if infoResp.StatusCode != 404 {
		t.Fatalf("expected 404 after file delete, got %d", infoResp.StatusCode)
	}
}

func TestRenameFileKeepsUserShareValid(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	_ = loginAs("alice", "pass")
	mustCreateReadyEncryptedFile(t, repos, "root", "/home/root/rename-me.txt", "rename-me.txt", 1)

	shareResp := createShareRequest(t, app, cookie, map[string]any{
		"path":            "/home/root/rename-me.txt",
		"target_username": "alice",
	})
	if shareResp.StatusCode != 200 {
		t.Fatalf("create share: expected 200, got %d", shareResp.StatusCode)
	}
	var shareResult map[string]any
	_ = json.NewDecoder(shareResp.Body).Decode(&shareResult)
	shareID, _ := shareResult["share_id"].(string)
	if shareID == "" {
		t.Fatal("expected share_id")
	}

	renameBody, _ := json.Marshal(map[string]any{
		"old_path": "/home/root/rename-me.txt",
		"new_path": "/home/root/renamed.txt",
		"is_dir":   false,
	})
	renameReq := httptest.NewRequest("POST", "/file/rename", bytes.NewReader(renameBody))
	renameReq.Header.Set("Content-Type", "application/json")
	renameReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	renameResp, err := app.Test(renameReq)
	if err != nil {
		t.Fatal(err)
	}
	if renameResp.StatusCode != 200 {
		t.Fatalf("rename file: expected 200, got %d", renameResp.StatusCode)
	}

	aliceCookie := loginAs("alice", "pass")
	infoReq := httptest.NewRequest("GET", "/file/shared/"+shareID, nil)
	infoReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: aliceCookie})
	infoResp, err := app.Test(infoReq)
	if err != nil {
		t.Fatal(err)
	}
	if infoResp.StatusCode != 200 {
		t.Fatalf("share info after rename: expected 200, got %d", infoResp.StatusCode)
	}
	var info map[string]any
	_ = json.NewDecoder(infoResp.Body).Decode(&info)
	if info["name"] != "renamed.txt" {
		t.Fatalf("expected renamed.txt, got %v", info["name"])
	}
}
