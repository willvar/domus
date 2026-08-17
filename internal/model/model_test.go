package model

import (
	"context"
	"errors"
	"os"
	"sync"
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
		dsn = "host=localhost port=5432 user=postgres password= dbname=domus_test_model sslmode=disable"
	}
	testDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("skipping test: could not connect to PostgreSQL: %v", err)
	}
	if err := testDB.AutoMigrate(&User{}, &FileRecord{}, &DBSession{}, &Task{}, &AuditLog{}, &WorkspaceState{}, &Share{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	testDB.Exec("ALTER TABLE shares DROP COLUMN IF EXISTS share_type")
	testDB.Exec("DELETE FROM users")
	testDB.Exec("DELETE FROM files")
	testDB.Exec("DELETE FROM sessions")
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

func TestFileGenerationCASAndLogicalMove(t *testing.T) {
	_, repos := setupTestDB(t)
	user, _ := repos.Users.Create("generation-owner", "pass", "user", "")
	oldPath := user.Username + "/home/" + user.Username + "/note.txt"
	if err := repos.Files.Upsert(user.ID, oldPath, "note.txt", false, 4, "text/plain", "hash"); err != nil {
		t.Fatal(err)
	}
	record, err := repos.Files.Get(user.ID, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 1 || record.StorageKey() != oldPath {
		t.Fatalf("unexpected initial generation: %+v", record)
	}

	target := DOFSGenerationObjectKey(user.ID, record.ID, 2, "tx")
	switched, err := repos.Files.CommitGeneration(user.ID, record.ID, 1, target, 9)
	if err != nil || !switched {
		t.Fatalf("commit generation: switched=%v err=%v", switched, err)
	}
	if switched, err := repos.Files.CommitGeneration(user.ID, record.ID, 1, target+"-stale", 10); err != nil || switched {
		t.Fatalf("stale CAS: switched=%v err=%v", switched, err)
	}

	newPath := user.Username + "/home/" + user.Username + "/renamed.txt"
	if err := repos.Files.Move(user.ID, oldPath, newPath, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	moved, err := repos.Files.Get(user.ID, newPath)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Generation != 2 || moved.StorageKey() != target || moved.LegacyObjectKey != oldPath || moved.Size != 9 {
		t.Fatalf("logical move changed immutable generation: %+v", moved)
	}
}

func TestThumbnailRecordsRetireTransactionally(t *testing.T) {
	_, repos := setupTestDB(t)
	user, err := repos.Users.Create("thumbnail-owner", "pass", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	root := user.Username + "/home/" + user.Username + "/"
	sourcePath := root + "photo.jpg"
	firstPath := root + ".user/thumbnails/first.webp"
	secondPath := root + ".user/derived/second.jpg"
	if err := repos.Files.Upsert(user.ID, sourcePath, "photo.jpg", false, 10, "image/jpeg", ""); err != nil {
		t.Fatal(err)
	}
	if err := repos.Files.Upsert(user.ID, firstPath, "first.webp", false, 2, "image/webp", ""); err != nil {
		t.Fatal(err)
	}
	first, err := repos.Files.Get(user.ID, firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Files.Upsert(
		user.ID, secondPath, "second.jpg", false, 3, "image/jpeg", "",
		UpsertFileOpts{ObjectKey: DOFSGenerationObjectKey(user.ID, first.ID+1, 1, "preview")},
	); err != nil {
		t.Fatal(err)
	}
	second, err := repos.Files.Get(user.ID, secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Files.UpdateThumbnail(user.ID, sourcePath, first.StorageKey(), "wrapped-1", 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := repos.Files.UpdateThumbnail(user.ID, sourcePath, second.StorageKey(), "wrapped-2", 20, 20, 0); err != nil {
		t.Fatal(err)
	}
	retiredFirst, err := repos.Files.GetByID(user.ID, first.ID)
	if err != nil || retiredFirst.Status != "deleted" {
		t.Fatalf("replaced thumbnail = %+v, %v", retiredFirst, err)
	}
	source, err := repos.Files.Get(user.ID, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	target := DOFSGenerationObjectKey(user.ID, source.ID, source.Generation+1, "edit")
	switched, err := repos.Files.CommitGeneration(user.ID, source.ID, source.Generation, target, 11)
	if err != nil || !switched {
		t.Fatalf("CommitGeneration() = %v, %v", switched, err)
	}
	current, err := repos.Files.GetByID(user.ID, source.ID)
	if err != nil || current.ThumbnailKey != "" || current.ThumbnailWrappedDEK != "" {
		t.Fatalf("updated source = %+v, %v", current, err)
	}
	retiredSecond, err := repos.Files.GetByStorageKey(user.ID, second.StorageKey())
	if err != nil || retiredSecond.ID != second.ID || retiredSecond.Status != "deleted" {
		t.Fatalf("generation thumbnail = %+v, %v", retiredSecond, err)
	}
}

func TestDOFSNamespaceTransactions(t *testing.T) {
	_, repos := setupTestDB(t)
	user, err := repos.Users.Create("dofs-owner", "pass", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	root := user.Username + "/home/" + user.Username + "/"
	directory, err := repos.Files.CreateDOFSNode(user.ID, root+"projects/", "projects", "", true)
	if err != nil {
		t.Fatal(err)
	}
	file, err := repos.Files.CreateDOFSNode(user.ID, directory.Path+"note.txt", "note.txt", "wrapped", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Files.CreateDOFSNode(user.ID, directory.Path+"note.txt/", "note.txt", "", true); !errors.Is(err, ErrFileExists) {
		t.Fatalf("cross-type sibling collision error = %v", err)
	}
	physical := DOFSGenerationObjectKey(user.ID, file.ID, 1, "create")
	if published, err := repos.Files.FinalizeDOFSFile(user.ID, file.ID, physical); err != nil || !published {
		t.Fatalf("finalize file: published=%v err=%v", published, err)
	}

	newDirectoryPath := root + "archive/"
	renamed, replaced, err := repos.Files.RenameDOFSNode(user.ID, directory.Path, newDirectoryPath, "archive", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if replaced != nil || renamed.ID != directory.ID || renamed.Path != newDirectoryPath {
		t.Fatalf("unexpected rename result: renamed=%+v replaced=%+v", renamed, replaced)
	}
	movedFile, err := repos.Files.Get(user.ID, newDirectoryPath+"note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if movedFile.ID != file.ID || movedFile.StorageKey() != physical {
		t.Fatalf("rename changed inode/object identity: %+v", movedFile)
	}
	if _, err := repos.Files.RemoveDOFSNode(user.ID, newDirectoryPath, true); !errors.Is(err, ErrDirectoryNotEmpty) {
		t.Fatalf("non-empty rmdir error = %v", err)
	}
	tombstone, err := repos.Files.RemoveDOFSNode(user.ID, movedFile.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if tombstone.Status != "deleted" || tombstone.StorageKey() != physical {
		t.Fatalf("unexpected tombstone: %+v", tombstone)
	}
	if switched, err := repos.Files.CommitGeneration(user.ID, file.ID, 1, physical+"-open-write", 7); err != nil || !switched {
		t.Fatalf("commit through unlinked inode: switched=%v err=%v", switched, err)
	}
	if purged, err := repos.Files.PurgeDeletedDOFSNode(user.ID, file.ID); err != nil || !purged {
		t.Fatalf("purge tombstone: purged=%v err=%v", purged, err)
	}
	if _, err := repos.Files.RemoveDOFSNode(user.ID, newDirectoryPath, true); err != nil {
		t.Fatalf("remove empty directory: %v", err)
	}
}

func TestDOFSNamespaceConcurrentCrossTypeCreateIsSerialized(t *testing.T) {
	testDB, repos := setupTestDB(t)
	// A previous development build may have created this unreleased unique
	// index. The production-compatible implementation must not depend on it.
	if err := testDB.Exec("DROP INDEX IF EXISTS idx_file_user_parent_name").Error; err != nil {
		t.Fatal(err)
	}
	user, err := repos.Users.Create("dofs-create-race", "pass", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	parent := user.Username + "/home/" + user.Username + "/"
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var workers sync.WaitGroup
	for _, directory := range []bool{false, true} {
		workers.Add(1)
		go func(isDir bool) {
			defer workers.Done()
			<-start
			filePath := parent + "same-name"
			if isDir {
				filePath += "/"
			}
			_, createErr := repos.Files.CreateDOFSNode(user.ID, filePath, "same-name", "wrapped", isDir)
			errorsCh <- createErr
		}(directory)
	}
	close(start)
	workers.Wait()
	close(errorsCh)

	var created, conflicts int
	for createErr := range errorsCh {
		switch {
		case createErr == nil:
			created++
		case errors.Is(createErr, ErrFileExists):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent create error: %v", createErr)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("created=%d conflicts=%d, want 1/1", created, conflicts)
	}
}

func TestDOFSWritableMountLeaseIsExclusiveAndReleasable(t *testing.T) {
	_, repos := setupTestDB(t)
	user, err := repos.Users.Create("dofs-lease-owner", "pass", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := repos.Files.AcquireDOFSMountLease(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	checker, ok := first.(interface{ Check(context.Context) error })
	if !ok {
		_ = first.Close()
		t.Fatal("PostgreSQL DOFS lease does not expose a health check")
	}
	checkContext, cancelCheck := context.WithTimeout(context.Background(), time.Second)
	if err := checker.Check(checkContext); err != nil {
		cancelCheck()
		_ = first.Close()
		t.Fatalf("lease health check: %v", err)
	}
	cancelCheck()
	if _, err := repos.Files.AcquireDOFSMountLease(user.ID); !errors.Is(err, ErrDOFSMountBusy) {
		_ = first.Close()
		t.Fatalf("second lease error = %v, want ErrDOFSMountBusy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	closedCheckContext, cancelClosedCheck := context.WithTimeout(context.Background(), time.Second)
	if err := checker.Check(closedCheckContext); err == nil {
		cancelClosedCheck()
		t.Fatal("closed lease health check unexpectedly succeeded")
	}
	cancelClosedCheck()
	second, err := repos.Files.AcquireDOFSMountLease(user.ID)
	if err != nil {
		t.Fatalf("lease after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
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
	trashPath := owner.Username + "/__trash__/home/" + owner.Username + "/doc.txt"
	if err := repos.Files.Upsert(owner.ID, filePath, "doc.txt", false, 123, "text/plain", "hash"); err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if err := repos.Files.Upsert(owner.ID, trashPath, "doc.txt", false, 123, "text/plain", "hash"); err != nil {
		t.Fatalf("upsert trash file: %v", err)
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
	if _, err := repos.Files.Get(owner.ID, trashPath); err == nil {
		t.Fatal("expected trash file record to be deleted")
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
