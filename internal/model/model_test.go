package model

import (
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password= dbname=zephyr_test sslmode=disable"
	}
	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("skipping test: could not connect to PostgreSQL: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &TrashItem{}, &UploadRecord{}, &Bookmark{}, &FileRecord{}, &DBSession{}, &Job{}, &AuditLog{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	db.Exec("DELETE FROM users")
	db.Exec("DELETE FROM trash")
	db.Exec("DELETE FROM uploads")
	db.Exec("DELETE FROM bookmarks")
	db.Exec("DELETE FROM files")
	db.Exec("DELETE FROM sessions")
	db.Exec("DELETE FROM jobs")
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

func TestDefaultPermissions(t *testing.T) {
	tests := []struct {
		role     string
		expected int64
	}{
		{"admin", PermAll},
		{"user", PermAll},
		{"unknown", PermAll},
	}
	for _, tt := range tests {
		got := DefaultPermissions(tt.role)
		if got != tt.expected {
			t.Errorf("DefaultPermissions(%q) = %d, want %d", tt.role, got, tt.expected)
		}
	}
}

func TestCreateUser(t *testing.T) {
	setupTestDB(t)
	user, err := CreateUser("alice", "password123", "admin", PermAll)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("expected alice, got %s", user.Username)
	}
	if user.Role != "admin" {
		t.Fatalf("expected admin, got %s", user.Role)
	}
	if user.PasswordHash == "" {
		t.Fatal("password hash should not be empty")
	}
}

func TestCreateUserDuplicateUsername(t *testing.T) {
	setupTestDB(t)
	_, err := CreateUser("alice", "pass1", "user", PermAll)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = CreateUser("alice", "pass2", "user", PermAll)
	if err == nil {
		t.Fatal("expected error for duplicate username")
	}
}

func TestGetUserByUsername(t *testing.T) {
	setupTestDB(t)
	_, _ = CreateUser("bob", "pass", "user", PermAll)
	user, err := GetUserByUsername("bob")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.Username != "bob" {
		t.Fatalf("expected bob, got %s", user.Username)
	}
}

func TestGetUserByID(t *testing.T) {
	setupTestDB(t)
	created, _ := CreateUser("charlie", "pass", "user", PermAll)
	user, err := GetUserByID(created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.Username != "charlie" {
		t.Fatalf("expected charlie, got %s", user.Username)
	}
}

func TestUpdateUser(t *testing.T) {
	setupTestDB(t)
	user, _ := CreateUser("dave", "pass", "user", PermAll)
	err := UpdateUser(user.ID, "admin", PermRead)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := GetUserByID(user.ID)
	if updated.Role != "admin" {
		t.Fatalf("expected admin, got %s", updated.Role)
	}
	if updated.Permissions != PermRead {
		t.Fatalf("expected permissions %d, got %d", PermRead, updated.Permissions)
	}
}

func TestUpdateUserPassword(t *testing.T) {
	setupTestDB(t)
	user, _ := CreateUser("eve", "oldpass", "user", PermAll)
	err := UpdateUserPassword(user.ID, "newpass")
	if err != nil {
		t.Fatalf("update password: %v", err)
	}
	updated, _ := GetUserByID(user.ID)
	if !CheckPassword(updated.PasswordHash, "newpass") {
		t.Fatal("new password should work")
	}
	if CheckPassword(updated.PasswordHash, "oldpass") {
		t.Fatal("old password should not work")
	}
}

func TestDeleteUser(t *testing.T) {
	setupTestDB(t)
	user, _ := CreateUser("frank", "pass", "user", PermAll)
	err := DeleteUser(user.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = GetUserByID(user.ID)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestUserCount(t *testing.T) {
	setupTestDB(t)
	count, err := UserCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
	_, _ = CreateUser("user1", "pass", "user", PermAll)
	_, _ = CreateUser("user2", "pass", "user", PermAll)
	count, _ = UserCount()
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

// --- Session tests ---

func TestSessionStore_CreateAndGet(t *testing.T) {
	setupTestDB(t)
	store := NewSessionStore(db)

	id, err := store.Create("user-uuid-1", "alice", "admin", PermAll)
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
	if session.Role != "admin" {
		t.Fatalf("expected role admin, got %s", session.Role)
	}
	if session.Permissions != PermAll {
		t.Fatalf("expected permissions %d, got %d", PermAll, session.Permissions)
	}
}

func TestSessionStore_GetExpired(t *testing.T) {
	setupTestDB(t)
	store := NewSessionStore(db)

	id, err := store.Create("user-uuid-1", "alice", "admin", PermAll)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	db.Model(&DBSession{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-1*time.Hour))
	session := store.Get(id)
	if session != nil {
		t.Fatal("expected nil for expired session")
	}
}

func TestSessionStore_GetNotFound(t *testing.T) {
	setupTestDB(t)
	store := NewSessionStore(db)
	session := store.Get("nonexistent-id")
	if session != nil {
		t.Fatal("expected nil for nonexistent session")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	setupTestDB(t)
	store := NewSessionStore(db)
	id, _ := store.Create("user-uuid-1", "alice", "admin", PermAll)
	store.Delete(id)
	session := store.Get(id)
	if session != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSessionStore_DeleteByUserID(t *testing.T) {
	setupTestDB(t)
	store := NewSessionStore(db)
	id1, _ := store.Create("user-uuid-1", "alice", "admin", PermAll)
	id2, _ := store.Create("user-uuid-1", "alice", "admin", PermAll)
	id3, _ := store.Create("user-uuid-2", "bob", "user", PermAll)
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
	setupTestDB(t)
	store := NewSessionStore(db)
	validID, _ := store.Create("user-uuid-1", "alice", "admin", PermAll)
	expiredID, _ := store.Create("user-uuid-2", "bob", "user", PermAll)
	db.Model(&DBSession{}).Where("id = ?", expiredID).Update("expires_at", time.Now().Add(-1*time.Hour))
	CleanExpiredSessions()
	if store.Get(validID) == nil {
		t.Fatal("valid session should still exist")
	}
	var count int64
	db.Model(&DBSession{}).Where("id = ?", expiredID).Count(&count)
	if count != 0 {
		t.Fatal("expired session should be cleaned up")
	}
}
