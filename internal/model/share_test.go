package model

import (
	"testing"
	"time"
)

func setupShareTestDB(t *testing.T) *Repos {
	t.Helper()
	testDB, repos := setupTestDB(t)
	if err := testDB.AutoMigrate(&Share{}); err != nil {
		t.Fatalf("failed to migrate Share: %v", err)
	}
	testDB.Exec("ALTER TABLE shares DROP COLUMN IF EXISTS share_type")
	testDB.Exec("DELETE FROM shares")
	return repos
}

func TestCreateShare(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "share-uuid-1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/docs/file.txt",
		FileName:     "file.txt",
		FileSize:     1024,
		TargetUserID: "target-1",
		WrappedDEK:   "aabbccdd",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if share.ID == 0 {
		t.Fatal("expected auto-generated ID")
	}
}

func TestGetShareByID(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "share-uuid-2",
		OwnerID:      "owner-1",
		FilePath:     "owner1/docs/file.txt",
		FileName:     "file.txt",
		FileSize:     2048,
		TargetUserID: "target-1",
		WrappedDEK:   "aabbccdd",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, err := repos.Shares.GetByID("share-uuid-2")
	if err != nil {
		t.Fatalf("GetShareByID: %v", err)
	}
	if got.OwnerID != "owner-1" {
		t.Fatalf("expected owner-1, got %s", got.OwnerID)
	}
	if got.FileName != "file.txt" {
		t.Fatalf("expected file.txt, got %s", got.FileName)
	}
}

func TestGetShareByID_NotFound(t *testing.T) {
	repos := setupShareTestDB(t)

	_, err := repos.Shares.GetByID("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent share")
	}
}

func TestListSharesForFile(t *testing.T) {
	repos := setupShareTestDB(t)

	shares := []Share{
		{ShareID: "s1", OwnerID: "owner-1", FilePath: "owner1/file.txt", FileName: "file.txt", TargetUserID: "user-1", WrappedDEK: "aa", Permission: "read"},
		{ShareID: "s2", OwnerID: "owner-1", FilePath: "owner1/file.txt", FileName: "file.txt", TargetUserID: "user-2", WrappedDEK: "bb", Permission: "read"},
		{ShareID: "s3", OwnerID: "owner-1", FilePath: "owner1/other.txt", FileName: "other.txt", TargetUserID: "user-3", WrappedDEK: "cc", Permission: "read"},
	}
	for i := range shares {
		if err := repos.Shares.Create(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	got, err := repos.Shares.ListForFile("owner-1", "owner1/file.txt")
	if err != nil {
		t.Fatalf("ListSharesForFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 shares for file, got %d", len(got))
	}
}

func TestListSharesForUser(t *testing.T) {
	repos := setupShareTestDB(t)

	shares := []Share{
		{ShareID: "u1", OwnerID: "owner-1", FilePath: "owner1/a.txt", FileName: "a.txt", TargetUserID: "target-1", WrappedDEK: "aa", Permission: "read"},
		{ShareID: "u2", OwnerID: "owner-2", FilePath: "owner2/b.txt", FileName: "b.txt", TargetUserID: "target-1", WrappedDEK: "bb", Permission: "read"},
		{ShareID: "u4", OwnerID: "owner-1", FilePath: "owner1/d.txt", FileName: "d.txt", TargetUserID: "target-2", WrappedDEK: "dd", Permission: "read"},
	}
	for i := range shares {
		if err := repos.Shares.Create(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	got, err := repos.Shares.ListForUser("target-1")
	if err != nil {
		t.Fatalf("ListSharesForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 shares for target-1, got %d", len(got))
	}
}

func TestListSharesForUserFiltersExpired(t *testing.T) {
	repos := setupShareTestDB(t)

	pastTime := time.Now().Add(-1 * time.Hour)
	futureTime := time.Now().Add(24 * time.Hour)
	shares := []Share{
		{ShareID: "exp1", OwnerID: "owner-1", FilePath: "owner1/a.txt", FileName: "a.txt", TargetUserID: "target-1", WrappedDEK: "aa", Permission: "read", ExpiresAt: &pastTime},
		{ShareID: "exp2", OwnerID: "owner-1", FilePath: "owner1/b.txt", FileName: "b.txt", TargetUserID: "target-1", WrappedDEK: "bb", Permission: "read", ExpiresAt: &futureTime},
		{ShareID: "exp3", OwnerID: "owner-1", FilePath: "owner1/c.txt", FileName: "c.txt", TargetUserID: "target-1", WrappedDEK: "cc", Permission: "read"},
	}
	for i := range shares {
		if err := repos.Shares.Create(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	got, err := repos.Shares.ListForUser("target-1")
	if err != nil {
		t.Fatalf("ListSharesForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 non-expired shares, got %d", len(got))
	}
}

func TestDeleteShare(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "del-1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/file.txt",
		FileName:     "file.txt",
		TargetUserID: "target-1",
		WrappedDEK:   "aa",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	deleted, err := repos.Shares.Delete(share.ID, "owner-1")
	if err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}
	if !deleted {
		t.Fatal("expected share to be deleted")
	}

	_, err = repos.Shares.GetByID("del-1")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestDeleteShare_TargetUserCanDelete(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "del-target-1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/file.txt",
		FileName:     "file.txt",
		TargetUserID: "target-1",
		WrappedDEK:   "aa",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	deleted, err := repos.Shares.Delete(share.ID, "target-1")
	if err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}
	if !deleted {
		t.Fatal("expected target user to be able to delete share")
	}

	_, err = repos.Shares.GetByID("del-target-1")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestDeleteShare_UnrelatedUser(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "del-2",
		OwnerID:      "owner-1",
		FilePath:     "owner1/file.txt",
		FileName:     "file.txt",
		TargetUserID: "target-1",
		WrappedDEK:   "aa",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	deleted, err := repos.Shares.Delete(share.ID, "unrelated-user")
	if err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}
	if deleted {
		t.Fatal("expected no delete for unrelated user")
	}

	got, err := repos.Shares.GetByID("del-2")
	if err != nil {
		t.Fatalf("share should still exist: %v", err)
	}
	if got.ShareID != "del-2" {
		t.Fatalf("expected del-2, got %s", got.ShareID)
	}
}

func TestExpiredShare(t *testing.T) {
	repos := setupShareTestDB(t)

	pastTime := time.Now().Add(-1 * time.Hour)
	share := &Share{
		ShareID:      "expired-1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/file.txt",
		FileName:     "file.txt",
		TargetUserID: "target-1",
		WrappedDEK:   "aa",
		Permission:   "read",
		ExpiresAt:    &pastTime,
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, err := repos.Shares.GetByID("expired-1")
	if err != nil {
		t.Fatalf("GetShareByID: %v", err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	if got.ExpiresAt.After(time.Now()) {
		t.Fatal("expected ExpiresAt to be in the past")
	}
}

func TestDeleteSharesByPrefix(t *testing.T) {
	repos := setupShareTestDB(t)

	shares := []Share{
		{ShareID: "p1", OwnerID: "owner-1", FilePath: "owner1/docs/a.txt", FileName: "a.txt", TargetUserID: "t1", WrappedDEK: "aa", Permission: "read"},
		{ShareID: "p2", OwnerID: "owner-1", FilePath: "owner1/docs/sub/b.txt", FileName: "b.txt", TargetUserID: "t2", WrappedDEK: "bb", Permission: "read"},
		{ShareID: "p3", OwnerID: "owner-1", FilePath: "owner1/other/c.txt", FileName: "c.txt", TargetUserID: "t3", WrappedDEK: "cc", Permission: "read"},
	}
	for i := range shares {
		if err := repos.Shares.Create(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	if err := repos.Shares.DeleteByPrefix("owner-1", "owner1/docs/"); err != nil {
		t.Fatalf("DeleteSharesByPrefix: %v", err)
	}

	if _, err := repos.Shares.GetByID("p1"); err == nil {
		t.Fatal("expected p1 to be deleted")
	}
	if _, err := repos.Shares.GetByID("p2"); err == nil {
		t.Fatal("expected p2 to be deleted")
	}
	if _, err := repos.Shares.GetByID("p3"); err != nil {
		t.Fatalf("expected p3 to remain, got err: %v", err)
	}
}

func TestMoveSharesByPath(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "m1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/old/file.txt",
		FileName:     "file.txt",
		TargetUserID: "t1",
		WrappedDEK:   "aa",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	if err := repos.Shares.MoveByPath("owner-1", "owner1/old/file.txt", "owner1/new/renamed.txt"); err != nil {
		t.Fatalf("MoveSharesByPath: %v", err)
	}

	got, err := repos.Shares.GetByID("m1")
	if err != nil {
		t.Fatalf("GetShareByID: %v", err)
	}
	if got.FilePath != "owner1/new/renamed.txt" {
		t.Fatalf("expected updated path, got %s", got.FilePath)
	}
	if got.FileName != "renamed.txt" {
		t.Fatalf("expected updated name, got %s", got.FileName)
	}
}

func TestMoveSharesByPrefix(t *testing.T) {
	repos := setupShareTestDB(t)

	shares := []Share{
		{ShareID: "mp1", OwnerID: "owner-1", FilePath: "owner1/docs/a.txt", FileName: "a.txt", TargetUserID: "t1", WrappedDEK: "aa", Permission: "read"},
		{ShareID: "mp2", OwnerID: "owner-1", FilePath: "owner1/docs/sub/b.txt", FileName: "b.txt", TargetUserID: "t2", WrappedDEK: "bb", Permission: "read"},
		{ShareID: "mp3", OwnerID: "owner-1", FilePath: "owner1/other/c.txt", FileName: "c.txt", TargetUserID: "t3", WrappedDEK: "cc", Permission: "read"},
	}
	for i := range shares {
		if err := repos.Shares.Create(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	if err := repos.Shares.MoveByPrefix("owner-1", "owner1/docs/", "owner1/archive/docs/"); err != nil {
		t.Fatalf("MoveSharesByPrefix: %v", err)
	}

	got1, err := repos.Shares.GetByID("mp1")
	if err != nil {
		t.Fatalf("GetShareByID mp1: %v", err)
	}
	if got1.FilePath != "owner1/archive/docs/a.txt" {
		t.Fatalf("unexpected mp1 path: %s", got1.FilePath)
	}
	got2, err := repos.Shares.GetByID("mp2")
	if err != nil {
		t.Fatalf("GetShareByID mp2: %v", err)
	}
	if got2.FilePath != "owner1/archive/docs/sub/b.txt" {
		t.Fatalf("unexpected mp2 path: %s", got2.FilePath)
	}
	got3, err := repos.Shares.GetByID("mp3")
	if err != nil {
		t.Fatalf("GetShareByID mp3: %v", err)
	}
	if got3.FilePath != "owner1/other/c.txt" {
		t.Fatalf("expected mp3 unchanged, got %s", got3.FilePath)
	}
}

func TestGetShareForUser(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "for-user-1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/docs/file.txt",
		FileName:     "file.txt",
		TargetUserID: "target-1",
		WrappedDEK:   "aa",
		Permission:   "read",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, err := repos.Shares.GetForUser("owner1/docs/file.txt", "target-1")
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if got.ShareID != "for-user-1" {
		t.Fatalf("expected for-user-1, got %s", got.ShareID)
	}

	if _, err := repos.Shares.GetForUser("owner1/docs/file.txt", "target-2"); err == nil {
		t.Fatal("expected error for wrong target user")
	}
	if _, err := repos.Shares.GetForUser("owner1/docs/missing.txt", "target-1"); err == nil {
		t.Fatal("expected error for wrong file path")
	}
}

func TestUpdateShareFileSize(t *testing.T) {
	repos := setupShareTestDB(t)

	share := &Share{
		ShareID:      "size-1",
		OwnerID:      "owner-1",
		FilePath:     "owner1/file.txt",
		FileName:     "file.txt",
		FileSize:     1024,
		TargetUserID: "target-1",
		WrappedDEK:   "aa",
		Permission:   "write",
	}
	if err := repos.Shares.Create(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	if err := repos.Shares.UpdateFileSize("size-1", 2048); err != nil {
		t.Fatalf("UpdateShareFileSize: %v", err)
	}

	got, err := repos.Shares.GetByID("size-1")
	if err != nil {
		t.Fatalf("GetShareByID: %v", err)
	}
	if got.FileSize != 2048 {
		t.Fatalf("expected 2048, got %d", got.FileSize)
	}
}
