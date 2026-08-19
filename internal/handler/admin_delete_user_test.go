package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/ws"
)

type userDeleteFixture struct {
	filePath        string
	ownedShareID    string
	incomingShareID string
	keepShareID     string
}

func seedUserDeleteFixture(t *testing.T, repos *model.Repos, victim, other, target *model.User) userDeleteFixture {
	t.Helper()

	filePath := "/doc.txt"
	trashPath := "/.domus/trash/doc.txt"
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
		t.Fatalf("save legacy workspace state: %v", err)
	}

	ownedShareID := "share-owned-" + victim.Username
	incomingShareID := "share-incoming-" + victim.Username
	keepShareID := "share-keep-" + victim.Username
	victimFile, err := repos.Files.Get(victim.ID, filePath)
	if err != nil {
		t.Fatalf("get victim file: %v", err)
	}

	for _, share := range []*model.Share{
		{
			ShareID: ownedShareID, OwnerID: victim.ID, FileInode: victimFile.ID,
			FilePath: filePath, FileName: "doc.txt", FileSize: 123, ContentType: "text/plain",
			TargetUserID: target.ID, WrappedDEK: "aa", Permission: "read",
		},
		{
			ShareID: incomingShareID, OwnerID: other.ID, FileInode: 1,
			FilePath: "/from-other.txt",
			FileName: "from-other.txt", FileSize: 1, ContentType: "text/plain",
			TargetUserID: victim.ID, WrappedDEK: "bb", Permission: "read",
		},
		{
			ShareID: keepShareID, OwnerID: other.ID, FileInode: 2,
			FilePath: "/keep.txt",
			FileName: "keep.txt", FileSize: 1, ContentType: "text/plain",
			TargetUserID: target.ID, WrappedDEK: "cc", Permission: "read",
		},
	} {
		if err := repos.Shares.Create(share); err != nil {
			t.Fatalf("create legacy share %s: %v", share.ShareID, err)
		}
	}

	return userDeleteFixture{
		filePath: filePath, ownedShareID: ownedShareID,
		incomingShareID: incomingShareID, keepShareID: keepShareID,
	}
}

func assertUserDeletedCompletely(t *testing.T, repos *model.Repos, victim *model.User, fixture userDeleteFixture) {
	t.Helper()

	if _, err := repos.Users.GetByID(victim.ID); err == nil {
		t.Fatal("expected user to be deleted")
	}
	if _, err := repos.Files.Get(victim.ID, fixture.filePath); err == nil {
		t.Fatal("expected file record to be deleted")
	}
	if _, err := repos.Files.Get(victim.ID, "/.domus/trash/doc.txt"); err == nil {
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
		t.Fatal("expected legacy workspace state to be deleted")
	}
	if _, err := repos.Shares.GetByID(fixture.ownedShareID); err == nil {
		t.Fatal("expected owned legacy share to be deleted")
	}
	if _, err := repos.Shares.GetByID(fixture.incomingShareID); err == nil {
		t.Fatal("expected incoming legacy share to be deleted")
	}
	if _, err := repos.Shares.GetByID(fixture.keepShareID); err != nil {
		t.Fatalf("expected unrelated legacy share to remain: %v", err)
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
	fixture := seedUserDeleteFixture(t, repos, victim, other, target)

	request := httptest.NewRequest(http.MethodDelete, "/audit/user/"+victim.ID, nil)
	request.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: adminCookie})
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/user/", nil)
	meRequest.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: victimCookie})
	meResponse, err := app.Test(meRequest)
	if err != nil {
		t.Fatal(err)
	}
	if meResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected victim session to be invalidated, got %d", meResponse.StatusCode)
	}

	assertUserDeletedCompletely(t, repos, victim, fixture)
}

func TestDeleteUserCompletelyWithoutDOFSLeavesNoApplicationIdentity(t *testing.T) {
	repos := model.NewMemRepos(nil)
	victim, _ := repos.Users.Create("victim-store-fail", "pass", "user", "")
	if err := repos.Files.Upsert(victim.ID, "/a.txt", "a.txt", false, 1, "text/plain", "h"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}

	handler := &Handler{Repos: repos, Store: &MockFileStore{}, Hub: ws.NewHub()}
	if err := handler.deleteUserCompletely(victim); err != nil {
		t.Fatalf("deleteUserCompletely: %v", err)
	}
	if _, err := repos.Users.GetByID(victim.ID); err == nil {
		t.Fatal("expected user to be deleted")
	}
}
