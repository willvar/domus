package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/ws"
)

type userDeleteFixture struct {
	filePath        string
	ownedShareID    string
	incomingShareID string
	keepShareID     string
}

func seedUserDeleteFixture(t *testing.T, repos *model.Repos, victim, other, target *model.User) userDeleteFixture {
	t.Helper()

	filePath := victim.Username + "/home/" + victim.Username + "/doc.txt"
	trashPath := victim.Username + "/__trash__/home/" + victim.Username + "/doc.txt"
	if err := repos.Files.Upsert(victim.ID, filePath, "doc.txt", false, 123, "text/plain", "hash"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if err := repos.Files.Upsert(victim.ID, trashPath, "doc.txt", false, 123, "text/plain", "hash"); err != nil {
		t.Fatalf("upsert trash file: %v", err)
	}
	if err := repos.Tasks.Create(victim.ID, "task-clean-"+victim.Username, "upload", "doc.txt"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := repos.Workspace.Save(victim.ID, `{"layout":"grid"}`); err != nil {
		t.Fatalf("save workspace state: %v", err)
	}

	ownedShareID := "share-owned-" + victim.Username
	incomingShareID := "share-incoming-" + victim.Username
	keepShareID := "share-keep-" + victim.Username

	if err := repos.Shares.Create(&model.Share{
		ShareID:      ownedShareID,
		OwnerID:      victim.ID,
		FilePath:     filePath,
		FileName:     "doc.txt",
		FileSize:     123,
		ContentType:  "text/plain",
		TargetUserID: target.ID,
		WrappedDEK:   "aa",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create owned share: %v", err)
	}
	if err := repos.Shares.Create(&model.Share{
		ShareID:      incomingShareID,
		OwnerID:      other.ID,
		FilePath:     other.Username + "/home/" + other.Username + "/from-other.txt",
		FileName:     "from-other.txt",
		FileSize:     1,
		ContentType:  "text/plain",
		TargetUserID: victim.ID,
		WrappedDEK:   "bb",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create incoming share: %v", err)
	}
	if err := repos.Shares.Create(&model.Share{
		ShareID:      keepShareID,
		OwnerID:      other.ID,
		FilePath:     other.Username + "/home/" + other.Username + "/keep.txt",
		FileName:     "keep.txt",
		FileSize:     1,
		ContentType:  "text/plain",
		TargetUserID: target.ID,
		WrappedDEK:   "cc",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create keep share: %v", err)
	}

	return userDeleteFixture{
		filePath:        filePath,
		ownedShareID:    ownedShareID,
		incomingShareID: incomingShareID,
		keepShareID:     keepShareID,
	}
}

func assertUserDeletedCompletely(t *testing.T, repos *model.Repos, victim *model.User, fx userDeleteFixture) {
	t.Helper()

	if _, err := repos.Users.GetByID(victim.ID); err == nil {
		t.Fatal("expected user to be deleted")
	}
	if _, err := repos.Files.Get(victim.ID, fx.filePath); err == nil {
		t.Fatal("expected file record to be deleted")
	}
	if _, err := repos.Files.Get(victim.ID, victim.Username+"/__trash__/home/"+victim.Username+"/doc.txt"); err == nil {
		t.Fatal("expected trash file record to be deleted")
	}
	tasks, err := repos.Tasks.ListRecent(victim.ID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks, got %d", len(tasks))
	}
	if _, err := repos.Workspace.Get(victim.ID); err == nil {
		t.Fatal("expected workspace state to be deleted")
	}
	if _, err := repos.Shares.GetByID(fx.ownedShareID); err == nil {
		t.Fatal("expected owned share to be deleted")
	}
	if _, err := repos.Shares.GetByID(fx.incomingShareID); err == nil {
		t.Fatal("expected incoming share to be deleted")
	}
	if _, err := repos.Shares.GetByID(fx.keepShareID); err != nil {
		t.Fatalf("expected unrelated share to remain: %v", err)
	}
}

func TestDeleteUserHTTPCleansRelatedData(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)

	adminCookie := loginAs("root", "pass")
	victimCookie := loginAs("victim-http", "pass")
	_ = loginAs("other-http", "pass")
	_ = loginAs("target-http", "pass")

	victim, err := repos.Users.GetByUsername("victim-http")
	if err != nil {
		t.Fatalf("get victim user: %v", err)
	}
	other, err := repos.Users.GetByUsername("other-http")
	if err != nil {
		t.Fatalf("get other user: %v", err)
	}
	target, err := repos.Users.GetByUsername("target-http")
	if err != nil {
		t.Fatalf("get target user: %v", err)
	}
	fx := seedUserDeleteFixture(t, repos, victim, other, target)

	req := httptest.NewRequest("DELETE", "/audit/user/"+victim.ID, nil)
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
		t.Fatalf("expected victim session to be invalidated, got %d", meResp.StatusCode)
	}

	assertUserDeletedCompletely(t, repos, victim, fx)
}

func TestDeleteUserCompletelyIgnoresStorageCleanupError(t *testing.T) {
	repos := model.NewMemRepos(nil)

	victim, _ := repos.Users.Create("victim-store-fail", "pass", "user", "")
	if err := repos.Files.Upsert(victim.ID, "victim-store-fail/home/victim-store-fail/a.txt", "a.txt", false, 1, "text/plain", "h"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}

	var calledPrefix string
	h := &Handler{
		Repos: repos,
		Store: &MockFileStore{
			RecursiveDeleteFn: func(prefix string, _ func(done, total int, current string)) error {
				calledPrefix = prefix
				return errors.New("mock storage failure")
			},
		},
		Hub: ws.NewHub(),
	}

	if err := h.deleteUserCompletely(victim); err != nil {
		t.Fatalf("deleteUserCompletely should not fail on storage cleanup error: %v", err)
	}
	if calledPrefix != victim.Username+"/" {
		t.Fatalf("expected cleanup prefix %q, got %q", victim.Username+"/", calledPrefix)
	}
	if _, err := repos.Users.GetByID(victim.ID); err == nil {
		t.Fatal("expected user to be deleted even when storage cleanup fails")
	}
}
