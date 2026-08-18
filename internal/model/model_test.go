package model

import (
	"os"
	"strings"
	"testing"
	"time"

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
	if err := database.AutoMigrate(&User{}, &DBSession{}, &Task{}, &AuditLog{}, &WorkspaceState{}, &Share{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	for _, statement := range []string{
		"DELETE FROM shares", "DELETE FROM tasks", "DELETE FROM workspace_states",
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
	for _, share := range []Share{
		{ShareID: "owned", OwnerID: owner.ID, FileInode: 1, FilePath: "owner/home/owner/a", FileName: "a", TargetUserID: target.ID, WrappedDEK: "aa", Permission: "read"},
		{ShareID: "incoming", OwnerID: other.ID, FileInode: 2, FilePath: "other/home/other/b", FileName: "b", TargetUserID: owner.ID, WrappedDEK: "bb", Permission: "read"},
		{ShareID: "keep", OwnerID: other.ID, FileInode: 3, FilePath: "other/home/other/c", FileName: "c", TargetUserID: target.ID, WrappedDEK: "cc", Permission: "read"},
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
