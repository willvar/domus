package model

import (
	"context"
	"fmt"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"domus/internal/notify"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) (*gorm.DB, *Repos) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres dbname=postgres sslmode=disable"
	}
	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("skipping test: could not connect to PostgreSQL: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Skipf("skipping test: could not open PostgreSQL pool: %v", err)
	}
	// SET is connection-local. Pin this test handle to one connection and keep
	// model tests isolated from other packages even when `go test ./...` runs
	// package binaries concurrently against the same TEST_DATABASE_DSN.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Exec("CREATE SCHEMA IF NOT EXISTS domus_test_model").Error; err != nil {
		t.Skipf("skipping test: could not create isolated schema: %v", err)
	}
	if err := database.Exec("SET search_path TO domus_test_model, public").Error; err != nil {
		t.Fatalf("select isolated schema: %v", err)
	}
	if err := revokeLegacyPathShares(database); err != nil {
		t.Fatalf("failed to revoke legacy shares: %v", err)
	}
	if err := database.AutoMigrate(&User{}, &DBSession{}, &Task{}, &AuditLog{}, &WorkspaceState{}, &Share{}, &TrashEntry{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	for _, statement := range []string{
		"DELETE FROM domus_trash_entries", "DELETE FROM shares", "DELETE FROM tasks", "DELETE FROM workspace_states",
		"DELETE FROM sessions", "DELETE FROM users",
	} {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("reset fixture with %q: %v", statement, err)
		}
	}
	return database, NewRepos(database, nil)
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("mypassword")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "mypassword") || CheckPassword(hash, "wrongpassword") {
		t.Fatal("password verification result is incorrect")
	}
}

func TestTranscodeClaimIsUserScopedAndFailureIsPersistent(t *testing.T) {
	_, repos := setupTestDB(t)
	if err := repos.Tasks.CreateQueued("other-user", "other-job", "transcode", "other", 1, "/other.mp4", "720p"); err != nil {
		t.Fatal(err)
	}
	if err := repos.Tasks.CreateQueued("selected-user", "selected-job", "transcode", "selected", 2, "/selected.mp4", "480p"); err != nil {
		t.Fatal(err)
	}
	claimed, err := repos.Tasks.ClaimNextTranscode("selected-user")
	if err != nil || claimed.TaskID != "selected-job" || claimed.UserID != "selected-user" {
		t.Fatalf("claimed another user's task: %#v %v", claimed, err)
	}
	other, err := repos.Tasks.Get("other-job")
	if err != nil || other.Status != "queued" {
		t.Fatal("unrelated task was changed")
	}
	if cancelled, err := repos.Tasks.CancelQueued(other.TaskID); err != nil || !cancelled {
		t.Fatal("queued cancellation was not atomic")
	}
	if _, err := repos.Tasks.ClaimNextTranscode("other-user"); err == nil {
		t.Fatal("cancelled task was claimed")
	}
	if cancelled, err := repos.Tasks.CancelQueued(claimed.TaskID); err != nil || cancelled {
		t.Fatal("a running task was cancelled as queued")
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("completed-upload-%d", i)
		if err := repos.Tasks.Create("selected-user", id, "upload", id); err != nil {
			t.Fatal(err)
		}
		if err := repos.Tasks.UpdateStatus(id, "completed"); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := repos.Tasks.ListRecent("selected-user")
	if err != nil || len(recent) == 0 || recent[0].TaskID != claimed.TaskID {
		t.Fatal("completed uploads obscured a running transcode")
	}
	if err := repos.Tasks.Fail(claimed.TaskID, "transcode_probe_failed"); err != nil {
		t.Fatal(err)
	}
	failed, err := repos.Tasks.Get(claimed.TaskID)
	if err != nil || failed.Error != "transcode_probe_failed" || failed.Status != "failed" {
		t.Fatal("failure was not retained")
	}
}

func TestTaskUpdatesNotifyIndependentListener(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_DSN for the cross-process notification check")
	}
	const taskID = "notification-task"
	if os.Getenv("DOMUS_TEST_NOTIFY_CHILD") == "1" {
		notify.Configure(os.Getenv("DOMUS_TEST_NOTIFY_SOCKET"))
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		sqlDB.SetMaxOpenConns(1)
		if err := db.Exec("SET search_path TO domus_test_model, public").Error; err != nil {
			t.Fatal(err)
		}
		repos := NewRepos(db, nil)
		if err := repos.Tasks.UpdateProgress(taskID, 0.37, "transcoding"); err != nil {
			t.Fatal(err)
		}
		if err := repos.Tasks.Fail(taskID, "transcode_encoding_failed"); err != nil {
			t.Fatal(err)
		}
		return
	}
	_, repos := setupTestDB(t)
	if err := repos.Tasks.CreateQueued("notification-user", taskID, "transcode", "sample.mp4", 1, "/sample.mp4", "720p"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	directory, err := os.MkdirTemp(os.TempDir(), "rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	socket := filepath.Join(directory, "events.sock")
	events := make(chan notify.Event, 4)
	listener, err := notify.Listen(ctx, socket, func(event notify.Event) {
		if event.Kind == "task" && event.UserID == "notification-user" && event.TaskID == taskID {
			select {
			case events <- event:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("notification endpoint is not owner-only")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, binary, "-test.run=^TestTaskUpdatesNotifyIndependentListener$")
	child.Env = append(os.Environ(), "DOMUS_TEST_NOTIFY_CHILD=1", "DOMUS_TEST_NOTIFY_SOCKET="+socket)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("independent publisher failed: %v\n%s", err, output)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-events:
		case <-ctx.Done():
			t.Fatal("independent process update was not delivered")
		}
	}
	task, err := repos.Tasks.Get(taskID)
	if err != nil || task.Status != "failed" || task.Error != "transcode_encoding_failed" {
		t.Fatal("notification arrived without persisted task state")
	}
	// A missing receiver must not turn a successfully stored update into failure.
	notify.Configure(filepath.Join(directory, "missing.sock"))
	t.Cleanup(func() { notify.Configure("") })
	if err := repos.Tasks.UpdateProgress(taskID, 0.38, "transcoding"); err != nil {
		t.Fatal(err)
	}
	task, err = repos.Tasks.Get(taskID)
	if err != nil || task.Progress != 0.38 {
		t.Fatal("failed hint changed persisted task state")
	}
	client, err := rpc.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Call("Events.NotifyChange", notify.Event{Kind: "unknown", UserID: "notification-user"}, &struct{}{}); err == nil {
		t.Fatal("receiver accepted an unknown event type")
	}
	client.Close()
	if extra, err := notify.Listen(ctx, socket, func(notify.Event) {}); err == nil {
		extra.Close()
		t.Fatal("a live event endpoint was replaced")
	}
	occupied := filepath.Join(directory, "occupied")
	if err := os.WriteFile(occupied, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if extra, err := notify.Listen(ctx, occupied, func(notify.Event) {}); err == nil {
		extra.Close()
		t.Fatal("a non-socket file was replaced")
	}
	if data, err := os.ReadFile(occupied); err != nil || string(data) != "keep" {
		t.Fatal("occupied path was modified")
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	restarted, err := notify.Listen(ctx, socket, func(notify.Event) {})
	if err != nil {
		t.Fatalf("stale socket prevented recovery: %v", err)
	}
	t.Cleanup(func() { restarted.Close() })
}

func TestLegacyFileSchemaFailsClosedWithoutDroppingData(t *testing.T) {
	database, _ := setupTestDB(t)
	if err := database.Exec(`CREATE TABLE files (id BIGSERIAL PRIMARY KEY, path TEXT NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Exec(`DROP TABLE IF EXISTS files`).Error })
	if err := database.Exec(`INSERT INTO files(path) VALUES ('keep-me')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := rejectLegacyFileSchema(database); err == nil || !strings.Contains(err.Error(), "no in-place file migration") {
		t.Fatalf("legacy schema error = %v", err)
	}
	var count int64
	if err := database.Table("files").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("legacy rows were modified: count=%d err=%v", count, err)
	}
}

func TestUserRepositoryLifecycleAndCounts(t *testing.T) {
	_, repos := setupTestDB(t)
	alice, err := repos.Users.Create("alice", "password", "user", "wrapped")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Users.Create("alice", "other", "user", "wrapped"); err == nil {
		t.Fatal("duplicate username was accepted")
	}
	if got, err := repos.Users.GetByUsername("alice"); err != nil || got.ID != alice.ID {
		t.Fatalf("lookup by username = %#v, %v", got, err)
	}
	if err := repos.Users.UpdateRole(alice.ID, "root"); err != nil {
		t.Fatal(err)
	}
	if err := repos.Users.UpdatePassword(alice.ID, "new-password"); err != nil {
		t.Fatal(err)
	}
	updated, err := repos.Users.GetByID(alice.ID)
	if err != nil || updated.Role != "root" || !CheckPassword(updated.PasswordHash, "new-password") {
		t.Fatalf("updated user = %#v, %v", updated, err)
	}
	if count, err := repos.Users.Count(); err != nil || count != 1 {
		t.Fatalf("user count = %d, %v", count, err)
	}
	if count, err := repos.Users.CountByRole("root"); err != nil || count != 1 {
		t.Fatalf("root count = %d, %v", count, err)
	}
	if err := repos.Users.Delete(alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Users.GetByID(alice.ID); err == nil {
		t.Fatal("deleted user remained visible")
	}
}

func TestDeleteUserAndRelatedApplicationData(t *testing.T) {
	database, repos := setupTestDB(t)
	owner, _ := repos.Users.Create("owner", "pass", "user", "wrapped")
	target, _ := repos.Users.Create("target", "pass", "user", "wrapped")
	other, _ := repos.Users.Create("other", "pass", "user", "wrapped")
	sessions := NewSessionStore(database)
	sessionID, err := sessions.Create(owner.ID, owner.Username, owner.Role)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Tasks.Create(owner.ID, "owner-task", "upload", "doc.txt"); err != nil {
		t.Fatal(err)
	}
	if err := repos.Workspace.Save(owner.ID, `{"layout":"test"}`); err != nil {
		t.Fatal(err)
	}
	if err := repos.Trash.Create(&TrashEntry{
		ID: "owner-trash", UserID: owner.ID, RootInode: 10, OriginalPath: "/doc.txt",
		OriginalName: "doc.txt", State: TrashStateReady,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Trash.Create(&TrashEntry{
		ID: "other-trash", UserID: other.ID, RootInode: 11, OriginalPath: "/keep.txt",
		OriginalName: "keep.txt", State: TrashStateReady,
	}); err != nil {
		t.Fatal(err)
	}
	for _, share := range []Share{
		{ShareID: "owned", OwnerID: owner.ID, FileInode: 1, FilePath: "/a", FileName: "a", TargetUserID: target.ID, WrappedDEK: "aa", Permission: "read"},
		{ShareID: "incoming", OwnerID: other.ID, FileInode: 2, FilePath: "/b", FileName: "b", TargetUserID: owner.ID, WrappedDEK: "bb", Permission: "read"},
		{ShareID: "keep", OwnerID: other.ID, FileInode: 3, FilePath: "/c", FileName: "c", TargetUserID: target.ID, WrappedDEK: "cc", Permission: "read"},
	} {
		copy := share
		if err := repos.Shares.Create(&copy); err != nil {
			t.Fatal(err)
		}
	}
	if err := repos.Cleanup.DeleteUserAndRelatedData(owner.ID); err != nil {
		t.Fatal(err)
	}
	if sessions.Get(sessionID) != nil {
		t.Fatal("session was not deleted")
	}
	if tasks, err := repos.Tasks.ListRecent(owner.ID); err != nil || len(tasks) != 0 {
		t.Fatalf("tasks = %#v, %v", tasks, err)
	}
	if _, err := repos.Workspace.Get(owner.ID); err == nil {
		t.Fatal("workspace state was not deleted")
	}
	if _, err := repos.Trash.Get(owner.ID, "owner-trash"); err == nil {
		t.Fatal("trash entry was not deleted")
	}
	if _, err := repos.Trash.Get(other.ID, "other-trash"); err != nil {
		t.Fatalf("unrelated trash entry was deleted: %v", err)
	}
	for _, deleted := range []string{"owned", "incoming"} {
		if _, err := repos.Shares.GetByID(deleted); err == nil {
			t.Fatalf("share %s was not deleted", deleted)
		}
	}
	if _, err := repos.Shares.GetByID("keep"); err != nil {
		t.Fatalf("unrelated share was deleted: %v", err)
	}
}

func TestSessionStoreLifecycleAndExpiry(t *testing.T) {
	database, repos := setupTestDB(t)
	user, _ := repos.Users.Create("session-user", "pass", "user", "wrapped")
	store := NewSessionStore(database)
	id, err := store.Create(user.ID, user.Username, user.Role)
	if err != nil {
		t.Fatal(err)
	}
	if session := store.Get(id); session == nil || session.UserID != user.ID {
		t.Fatalf("session = %#v", session)
	}
	store.Delete(id)
	if store.Get(id) != nil {
		t.Fatal("deleted session remained visible")
	}
	first, _ := store.Create(user.ID, user.Username, user.Role)
	second, _ := store.Create(user.ID, user.Username, user.Role)
	store.DeleteByUserID(user.ID)
	if store.Get(first) != nil || store.Get(second) != nil {
		t.Fatal("user sessions remained visible")
	}
	expired, _ := store.Create(user.ID, user.Username, user.Role)
	if err := database.Model(&DBSession{}).Where("id = ?", expired).Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if store.Get(expired) != nil {
		t.Fatal("expired session was returned")
	}
	store.CleanExpired()
}
