package model

import (
	"os"
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
		dsn = "host=localhost port=5432 user=postgres password= dbname=zephyr_test_model sslmode=disable"
	}
	testDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("skipping test: could not connect to PostgreSQL: %v", err)
	}
	if err := testDB.AutoMigrate(&User{}, &TrashItem{}, &FileRecord{}, &DBSession{}, &Job{}, &Task{}, &AuditLog{}, &WorkspaceState{}, &Share{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	testDB.Exec("DELETE FROM users")
	testDB.Exec("DELETE FROM trash")
	testDB.Exec("DELETE FROM files")
	testDB.Exec("DELETE FROM sessions")
	testDB.Exec("DELETE FROM jobs")
	testDB.Exec("DELETE FROM tasks")
	testDB.Exec("DELETE FROM workspace_states")
	testDB.Exec("DELETE FROM shares")
	repos := NewRepos(testDB, false, nil)
	return testDB, repos
}

// --- User tests ---

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("mypassword")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword(hash, "mypassword") {
		t.Fatal("expected password to match")
	}
	if CheckPassword(hash, "wrongpassword") {
		t.Fatal("expected password not to match")
	}
}

func TestCreateUser(t *testing.T) {
	_, repos := setupTestDB(t)
	user, err := repos.Users.Create("alice", "password123", "root", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("expected alice, got %s", user.Username)
	}
	if user.Role != "root" {
		t.Fatalf("expected admin, got %s", user.Role)
	}
	if user.PasswordHash == "" {
		t.Fatal("password hash should not be empty")
	}
}

func TestCreateUserDuplicateUsername(t *testing.T) {
	_, repos := setupTestDB(t)
	_, err := repos.Users.Create("alice", "pass1", "user", "")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = repos.Users.Create("alice", "pass2", "user", "")
	if err == nil {
		t.Fatal("expected error for duplicate username")
	}
}

func TestGetUserByUsername(t *testing.T) {
	_, repos := setupTestDB(t)
	_, _ = repos.Users.Create("bob", "pass", "user", "")
	user, err := repos.Users.GetByUsername("bob")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.Username != "bob" {
		t.Fatalf("expected bob, got %s", user.Username)
	}
}

func TestGetUserByID(t *testing.T) {
	_, repos := setupTestDB(t)
	created, _ := repos.Users.Create("charlie", "pass", "user", "")
	user, err := repos.Users.GetByID(created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.Username != "charlie" {
		t.Fatalf("expected charlie, got %s", user.Username)
	}
}

func TestUpdateUser(t *testing.T) {
	_, repos := setupTestDB(t)
	user, _ := repos.Users.Create("dave", "pass", "user", "")
	err := repos.Users.UpdateRole(user.ID, "root")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := repos.Users.GetByID(user.ID)
	if updated.Role != "root" {
		t.Fatalf("expected admin, got %s", updated.Role)
	}
}

func TestUpdateUserPassword(t *testing.T) {
	_, repos := setupTestDB(t)
	user, _ := repos.Users.Create("eve", "oldpass", "user", "")
	err := repos.Users.UpdatePassword(user.ID, "newpass")
	if err != nil {
		t.Fatalf("update password: %v", err)
	}
	updated, _ := repos.Users.GetByID(user.ID)
	if !CheckPassword(updated.PasswordHash, "newpass") {
		t.Fatal("new password should work")
	}
	if CheckPassword(updated.PasswordHash, "oldpass") {
		t.Fatal("old password should not work")
	}
}

func TestDeleteUser(t *testing.T) {
	_, repos := setupTestDB(t)
	user, _ := repos.Users.Create("frank", "pass", "user", "")
	err := repos.Users.Delete(user.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repos.Users.GetByID(user.ID)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestDeleteUserAndRelatedData(t *testing.T) {
	testDB, repos := setupTestDB(t)

	owner, _ := repos.Users.Create("owner", "pass", "user", "")
	target, _ := repos.Users.Create("target", "pass", "user", "")
	other, _ := repos.Users.Create("other", "pass", "user", "")

	store := NewSessionStore(testDB)
	sessionID, err := store.Create(owner.ID, owner.Username, owner.Role)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	filePath := owner.Username + "/home/" + owner.Username + "/doc.txt"
	if err := repos.Files.Upsert(owner.ID, filePath, "doc.txt", false, 123, "text/plain", "hash"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if err := repos.Trash.Create(owner.ID, filePath, owner.Username+"/.trash/doc.txt", 123, false); err != nil {
		t.Fatalf("create trash record: %v", err)
	}
	if _, err := repos.Jobs.Create(owner.ID, "job-clean-owner", "transcode", "{}"); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := repos.Tasks.Create(owner.ID, "task-clean-owner", "upload", "doc.txt"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := repos.Workspace.Save(owner.ID, `{"layout":"test"}`); err != nil {
		t.Fatalf("save workspace state: %v", err)
	}

	if err := repos.Shares.Create(&Share{
		ShareID:      "share-owner-clean",
		OwnerID:      owner.ID,
		FilePath:     filePath,
		FileName:     "doc.txt",
		FileSize:     123,
		ContentType:  "text/plain",
		TargetUserID: target.ID,
		WrappedDEK:   "aa",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create owner share: %v", err)
	}
	if err := repos.Shares.Create(&Share{
		ShareID:      "share-target-clean",
		OwnerID:      other.ID,
		FilePath:     other.Username + "/home/" + other.Username + "/x.txt",
		FileName:     "x.txt",
		FileSize:     1,
		ContentType:  "text/plain",
		TargetUserID: owner.ID,
		WrappedDEK:   "bb",
		Permission:   "read",
	}); err != nil {
		t.Fatalf("create target share: %v", err)
	}
	if err := repos.Shares.Create(&Share{
		ShareID:      "share-other-keep",
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

	if err := repos.Cleanup.DeleteUserAndRelatedData(owner.ID); err != nil {
		t.Fatalf("DeleteUserAndRelatedData: %v", err)
	}

	if _, err := repos.Users.GetByID(owner.ID); err == nil {
		t.Fatal("expected owner to be deleted")
	}
	if store.Get(sessionID) != nil {
		t.Fatal("expected owner session to be deleted")
	}
	if _, err := repos.Files.Get(owner.ID, filePath); err == nil {
		t.Fatal("expected owner file to be deleted")
	}
	trash, err := repos.Trash.List(owner.ID)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(trash) != 0 {
		t.Fatalf("expected no trash records, got %d", len(trash))
	}
	jobs, err := repos.Jobs.ListActive(owner.ID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs, got %d", len(jobs))
	}
	tasks, err := repos.Tasks.ListRecent(owner.ID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks, got %d", len(tasks))
	}
	if _, err := repos.Workspace.Get(owner.ID); err == nil {
		t.Fatal("expected workspace state to be deleted")
	}
	if _, err := repos.Shares.GetByID("share-owner-clean"); err == nil {
		t.Fatal("expected owner share to be deleted")
	}
	if _, err := repos.Shares.GetByID("share-target-clean"); err == nil {
		t.Fatal("expected target share to be deleted")
	}
	if _, err := repos.Shares.GetByID("share-other-keep"); err != nil {
		t.Fatalf("expected unrelated share to remain: %v", err)
	}
}

func TestUserCount(t *testing.T) {
	_, repos := setupTestDB(t)
	count, err := repos.Users.Count()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
	_, _ = repos.Users.Create("user1", "pass", "user", "")
	_, _ = repos.Users.Create("user2", "pass", "user", "")
	count, _ = repos.Users.Count()
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestCountUsersByRole(t *testing.T) {
	_, repos := setupTestDB(t)
	_, _ = repos.Users.Create("root-user", "pass", "root", "")
	_, _ = repos.Users.Create("normal-user-1", "pass", "user", "")
	_, _ = repos.Users.Create("normal-user-2", "pass", "user", "")

	rootCount, err := repos.Users.CountByRole("root")
	if err != nil {
		t.Fatalf("count root users: %v", err)
	}
	if rootCount != 1 {
		t.Fatalf("expected 1 root user, got %d", rootCount)
	}

	userCount, err := repos.Users.CountByRole("user")
	if err != nil {
		t.Fatalf("count normal users: %v", err)
	}
	if userCount != 2 {
		t.Fatalf("expected 2 normal users, got %d", userCount)
	}
}

// --- Session tests ---

func TestSessionStore_CreateAndGet(t *testing.T) {
	testDB, _ := setupTestDB(t)
	store := NewSessionStore(testDB)

	id, err := store.Create("user-uuid-1", "alice", "root")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session := store.Get(id)
	if session == nil {
		t.Fatal("expected session, got nil")
	}
	if session.UserID != "user-uuid-1" {
		t.Fatalf("expected UserID user-uuid-1, got %s", session.UserID)
	}
	if session.Username != "alice" {
		t.Fatalf("expected username alice, got %s", session.Username)
	}
	if session.Role != "root" {
		t.Fatalf("expected role admin, got %s", session.Role)
	}
}

func TestSessionStore_GetExpired(t *testing.T) {
	testDB, _ := setupTestDB(t)
	store := NewSessionStore(testDB)

	id, err := store.Create("user-uuid-1", "alice", "root")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	testDB.Model(&DBSession{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-1*time.Hour))
	session := store.Get(id)
	if session != nil {
		t.Fatal("expected nil for expired session")
	}
}

func TestSessionStore_GetNotFound(t *testing.T) {
	testDB, _ := setupTestDB(t)
	store := NewSessionStore(testDB)
	session := store.Get("nonexistent-id")
	if session != nil {
		t.Fatal("expected nil for nonexistent session")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	testDB, _ := setupTestDB(t)
	store := NewSessionStore(testDB)
	id, _ := store.Create("user-uuid-1", "alice", "root")
	store.Delete(id)
	session := store.Get(id)
	if session != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSessionStore_DeleteByUserID(t *testing.T) {
	testDB, _ := setupTestDB(t)
	store := NewSessionStore(testDB)
	id1, _ := store.Create("user-uuid-1", "alice", "root")
	id2, _ := store.Create("user-uuid-1", "alice", "root")
	id3, _ := store.Create("user-uuid-2", "bob", "user")
	store.DeleteByUserID("user-uuid-1")
	if store.Get(id1) != nil {
		t.Fatal("session 1 should be deleted")
	}
	if store.Get(id2) != nil {
		t.Fatal("session 2 should be deleted")
	}
	if store.Get(id3) == nil {
		t.Fatal("session 3 should still exist")
	}
}

func TestCleanExpiredSessions(t *testing.T) {
	testDB, _ := setupTestDB(t)
	store := NewSessionStore(testDB)
	validID, _ := store.Create("user-uuid-1", "alice", "root")
	expiredID, _ := store.Create("user-uuid-2", "bob", "user")
	testDB.Model(&DBSession{}).Where("id = ?", expiredID).Update("expires_at", time.Now().Add(-1*time.Hour))
	store.CleanExpired()
	if store.Get(validID) == nil {
		t.Fatal("valid session should still exist")
	}
	var count int64
	testDB.Model(&DBSession{}).Where("id = ?", expiredID).Count(&count)
	if count != 0 {
		t.Fatal("expired session should be cleaned up")
	}
}
