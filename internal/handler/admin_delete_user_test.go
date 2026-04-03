package handler

import (
	"encoding/json"
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

func seedUserDeleteFixture(t *testing.T, victim, other, target *model.User) userDeleteFixture {
	t.Helper()

	filePath := victim.Username + "/home/" + victim.Username + "/doc.txt"
	if err := model.UpsertFile(victim.ID, filePath, "doc.txt", false, 123, "text/plain", "hash"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if err := model.CreateTrashRecord(victim.ID, filePath, victim.Username+"/.trash/doc.txt", 123, false); err != nil {
		t.Fatalf("create trash record: %v", err)
	}
	if _, err := model.CreateJob(victim.ID, "job-clean-"+victim.Username, "transcode", "{}"); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := model.CreateTask(victim.ID, "task-clean-"+victim.Username, "upload", "doc.txt"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := model.SaveWorkspaceState(victim.ID, `{"layout":"grid"}`); err != nil {
		t.Fatalf("save workspace state: %v", err)
	}

	ownedShareID := "share-owned-" + victim.Username
	incomingShareID := "share-incoming-" + victim.Username
	keepShareID := "share-keep-" + victim.Username

	if err := model.CreateShare(&model.Share{
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
	if err := model.CreateShare(&model.Share{
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
	if err := model.CreateShare(&model.Share{
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

func assertUserDeletedCompletely(t *testing.T, victim *model.User, fx userDeleteFixture) {
	t.Helper()

	if _, err := model.GetUserByID(victim.ID); err == nil {
		t.Fatal("expected user to be deleted")
	}
	if _, err := model.GetFile(victim.ID, fx.filePath); err == nil {
		t.Fatal("expected file record to be deleted")
	}
	trash, err := model.ListTrash(victim.ID)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(trash) != 0 {
		t.Fatalf("expected no trash records, got %d", len(trash))
	}
	jobs, err := model.ListActiveJobs(victim.ID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no active jobs, got %d", len(jobs))
	}
	tasks, err := model.ListRecentTasks(victim.ID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks, got %d", len(tasks))
	}
	if _, err := model.GetWorkspaceState(victim.ID); err == nil {
		t.Fatal("expected workspace state to be deleted")
	}
	if _, err := model.GetShareByID(fx.ownedShareID); err == nil {
		t.Fatal("expected owned share to be deleted")
	}
	if _, err := model.GetShareByID(fx.incomingShareID); err == nil {
		t.Fatal("expected incoming share to be deleted")
	}
	if _, err := model.GetShareByID(fx.keepShareID); err != nil {
		t.Fatalf("expected unrelated share to remain: %v", err)
	}
}

func TestDeleteUserHTTPCleansRelatedData(t *testing.T) {
	app, loginAs := setupTestApp(t)

	adminCookie := loginAs("root", "pass")
	victimCookie := loginAs("victim-http", "pass")
	_ = loginAs("other-http", "pass")
	_ = loginAs("target-http", "pass")

	victim, _ := model.GetUserByUsername("victim-http")
	other, _ := model.GetUserByUsername("other-http")
	target, _ := model.GetUserByUsername("target-http")
	fx := seedUserDeleteFixture(t, victim, other, target)

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

	assertUserDeletedCompletely(t, victim, fx)
}

func TestDeleteUserWSCleansRelatedData(t *testing.T) {
	testDB := setupTestDB(t)
	model.SetDB(testDB)

	sessions := model.NewSessionStore(testDB)
	audit := model.NewAuditWorker(testDB)
	audit.Start()

	h := &Handler{
		DB:       testDB,
		Store:    &MockFileStore{},
		Sessions: sessions,
		Audit:    audit,
		Hub:      ws.NewHub(),
	}

	admin, _ := model.CreateUser("admin-ws", "pass", "root", "")
	victim, _ := model.CreateUser("victim-ws", "pass", "user", "")
	other, _ := model.CreateUser("other-ws", "pass", "user", "")
	target, _ := model.CreateUser("target-ws", "pass", "user", "")
	fx := seedUserDeleteFixture(t, victim, other, target)

	victimSessionID, err := sessions.Create(victim.ID, victim.Username, victim.Role)
	if err != nil {
		t.Fatalf("create victim session: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"id": victim.ID})
	conn := &ws.Conn{
		Session: &model.Session{
			UserID:   admin.ID,
			Username: admin.Username,
			Role:     "root",
		},
	}

	result, err := h.wsAdminDeleteUser(conn, "", payload)
	if err != nil {
		t.Fatalf("wsAdminDeleteUser: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["ok"] != true {
		t.Fatalf("expected ok response, got %#v", result)
	}
	if sessions.Get(victimSessionID) != nil {
		t.Fatal("expected victim sessions to be deleted")
	}

	assertUserDeletedCompletely(t, victim, fx)
}

func TestDeleteUserCompletelyIgnoresStorageCleanupError(t *testing.T) {
	testDB := setupTestDB(t)
	model.SetDB(testDB)

	victim, _ := model.CreateUser("victim-store-fail", "pass", "user", "")
	if err := model.UpsertFile(victim.ID, "victim-store-fail/home/victim-store-fail/a.txt", "a.txt", false, 1, "text/plain", "h"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}

	var calledPrefix string
	h := &Handler{
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
	if _, err := model.GetUserByID(victim.ID); err == nil {
		t.Fatal("expected user to be deleted even when storage cleanup fails")
	}
}

func TestDeleteLastRootRejectedWS(t *testing.T) {
	testDB := setupTestDB(t)
	model.SetDB(testDB)

	sessions := model.NewSessionStore(testDB)
	audit := model.NewAuditWorker(testDB)
	audit.Start()

	h := &Handler{
		DB:       testDB,
		Store:    &MockFileStore{},
		Sessions: sessions,
		Audit:    audit,
		Hub:      ws.NewHub(),
	}

	lastRoot, _ := model.CreateUser("last-root", "pass", "root", "")
	payload, _ := json.Marshal(map[string]string{"id": lastRoot.ID})
	conn := &ws.Conn{
		Session: &model.Session{
			UserID:   "external-admin",
			Username: "external-admin",
			Role:     "root",
		},
	}

	_, err := h.wsAdminDeleteUser(conn, "", payload)
	if err == nil {
		t.Fatal("expected cannot_delete_last_root error")
	}
	wsErr, ok := err.(*wsError)
	if !ok {
		t.Fatalf("expected wsError, got %T", err)
	}
	if wsErr.Code != "cannot_delete_last_root" {
		t.Fatalf("expected cannot_delete_last_root, got %s", wsErr.Code)
	}

	if _, err := model.GetUserByID(lastRoot.ID); err != nil {
		t.Fatalf("expected last root to remain, got err: %v", err)
	}
}
