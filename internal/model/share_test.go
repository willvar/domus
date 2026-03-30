package model

import (
	"testing"
	"time"
)

func setupShareTestDB(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	if err := db.AutoMigrate(&Share{}); err != nil {
		t.Fatalf("failed to migrate Share: %v", err)
	}
	db.Exec("DELETE FROM shares")
}

func TestCreateShare(t *testing.T) {
	setupShareTestDB(t)

	share := &Share{
		ShareID:    "share-uuid-1",
		OwnerID:    "owner-1",
		FilePath:   "owner1/docs/file.txt",
		FileName:   "file.txt",
		FileSize:   1024,
		ShareType:  "link",
		WrappedDEK: "aabbccdd",
		Permission: "read",
	}
	if err := CreateShare(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if share.ID == 0 {
		t.Fatal("expected auto-generated ID")
	}
}

func TestGetShareByID(t *testing.T) {
	setupShareTestDB(t)

	share := &Share{
		ShareID:    "share-uuid-2",
		OwnerID:    "owner-1",
		FilePath:   "owner1/docs/file.txt",
		FileName:   "file.txt",
		FileSize:   2048,
		ShareType:  "link",
		WrappedDEK: "aabbccdd",
		Permission: "read",
	}
	if err := CreateShare(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, err := GetShareByID("share-uuid-2")
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
	setupShareTestDB(t)

	_, err := GetShareByID("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent share")
	}
}

func TestListSharesForFile(t *testing.T) {
	setupShareTestDB(t)

	shares := []Share{
		{ShareID: "s1", OwnerID: "owner-1", FilePath: "owner1/file.txt", FileName: "file.txt", ShareType: "link", WrappedDEK: "aa", Permission: "read"},
		{ShareID: "s2", OwnerID: "owner-1", FilePath: "owner1/file.txt", FileName: "file.txt", ShareType: "user", TargetUserID: "user-2", WrappedDEK: "bb", Permission: "read"},
		{ShareID: "s3", OwnerID: "owner-1", FilePath: "owner1/other.txt", FileName: "other.txt", ShareType: "link", WrappedDEK: "cc", Permission: "read"},
	}
	for i := range shares {
		if err := CreateShare(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	got, err := ListSharesForFile("owner-1", "owner1/file.txt")
	if err != nil {
		t.Fatalf("ListSharesForFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 shares for file, got %d", len(got))
	}
}

func TestListSharesForUser(t *testing.T) {
	setupShareTestDB(t)

	shares := []Share{
		{ShareID: "u1", OwnerID: "owner-1", FilePath: "owner1/a.txt", FileName: "a.txt", ShareType: "user", TargetUserID: "target-1", WrappedDEK: "aa", Permission: "read"},
		{ShareID: "u2", OwnerID: "owner-2", FilePath: "owner2/b.txt", FileName: "b.txt", ShareType: "user", TargetUserID: "target-1", WrappedDEK: "bb", Permission: "read"},
		{ShareID: "u3", OwnerID: "owner-1", FilePath: "owner1/c.txt", FileName: "c.txt", ShareType: "link", WrappedDEK: "cc", Permission: "read"},
		{ShareID: "u4", OwnerID: "owner-1", FilePath: "owner1/d.txt", FileName: "d.txt", ShareType: "user", TargetUserID: "target-2", WrappedDEK: "dd", Permission: "read"},
	}
	for i := range shares {
		if err := CreateShare(&shares[i]); err != nil {
			t.Fatalf("CreateShare[%d]: %v", i, err)
		}
	}

	got, err := ListSharesForUser("target-1")
	if err != nil {
		t.Fatalf("ListSharesForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 shares for target-1, got %d", len(got))
	}
}

func TestDeleteShare(t *testing.T) {
	setupShareTestDB(t)

	share := &Share{
		ShareID:    "del-1",
		OwnerID:    "owner-1",
		FilePath:   "owner1/file.txt",
		FileName:   "file.txt",
		ShareType:  "link",
		WrappedDEK: "aa",
		Permission: "read",
	}
	if err := CreateShare(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	if err := DeleteShare(share.ID, "owner-1"); err != nil {
		t.Fatalf("DeleteShare: %v", err)
	}

	_, err := GetShareByID("del-1")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestDeleteShare_WrongOwner(t *testing.T) {
	setupShareTestDB(t)

	share := &Share{
		ShareID:    "del-2",
		OwnerID:    "owner-1",
		FilePath:   "owner1/file.txt",
		FileName:   "file.txt",
		ShareType:  "link",
		WrappedDEK: "aa",
		Permission: "read",
	}
	if err := CreateShare(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	// Delete with wrong owner — should not actually remove the record
	_ = DeleteShare(share.ID, "wrong-owner")

	got, err := GetShareByID("del-2")
	if err != nil {
		t.Fatalf("share should still exist: %v", err)
	}
	if got.ShareID != "del-2" {
		t.Fatalf("expected del-2, got %s", got.ShareID)
	}
}

func TestExpiredShare(t *testing.T) {
	setupShareTestDB(t)

	pastTime := time.Now().Add(-1 * time.Hour)
	share := &Share{
		ShareID:    "expired-1",
		OwnerID:    "owner-1",
		FilePath:   "owner1/file.txt",
		FileName:   "file.txt",
		ShareType:  "link",
		WrappedDEK: "aa",
		Permission: "read",
		ExpiresAt:  &pastTime,
	}
	if err := CreateShare(share); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	got, err := GetShareByID("expired-1")
	if err != nil {
		t.Fatalf("GetShareByID: %v", err)
	}

	// The share record is returned (the model layer does not filter by expiry),
	// so the caller must check expiry. Verify the ExpiresAt was stored and is in the past.
	if got.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	if got.ExpiresAt.After(time.Now()) {
		t.Fatal("expected ExpiresAt to be in the past")
	}
}
