package model

import (
	"errors"
	"testing"
	"time"
)

func setupShareTestDB(t *testing.T) *Repos {
	t.Helper()
	_, repos := setupTestDB(t)
	return repos
}

func testShare(shareID, ownerID, targetID string, inode int64) Share {
	return Share{
		ShareID:      shareID,
		OwnerID:      ownerID,
		FileInode:    inode,
		FilePath:     "/file.txt",
		FileName:     "file.txt",
		FileSize:     1024,
		ContentType:  "text/plain",
		TargetUserID: targetID,
		WrappedDEK:   "aabbccdd",
		Permission:   "read",
	}
}

func TestShareCreateAndLookup(t *testing.T) {
	repos := setupShareTestDB(t)
	share := testShare("share-1", "owner-1", "target-1", 101)
	if err := repos.Shares.Create(&share); err != nil {
		t.Fatalf("create: %v", err)
	}
	if share.ID == 0 {
		t.Fatal("expected database ID")
	}
	byPublicID, err := repos.Shares.GetByID(share.ShareID)
	if err != nil || byPublicID.FileInode != 101 {
		t.Fatalf("lookup by share ID = %#v, %v", byPublicID, err)
	}
	byDatabaseID, err := repos.Shares.GetByDatabaseID(share.ID)
	if err != nil || byDatabaseID.ShareID != share.ShareID {
		t.Fatalf("lookup by database ID = %#v, %v", byDatabaseID, err)
	}
	if _, err := repos.Shares.GetByID("missing"); err == nil {
		t.Fatal("missing share was found")
	}
}

func TestShareCreateRequiresStableInode(t *testing.T) {
	repos := setupShareTestDB(t)
	share := testShare("invalid", "owner-1", "target-1", 0)
	if err := repos.Shares.Create(&share); !errors.Is(err, ErrInvalidShareInode) {
		t.Fatalf("create error = %v, want %v", err, ErrInvalidShareInode)
	}

	memory := NewMemRepos(nil)
	share = testShare("invalid-memory", "owner-1", "target-1", -1)
	if err := memory.Shares.Create(&share); !errors.Is(err, ErrInvalidShareInode) {
		t.Fatalf("memory create error = %v, want %v", err, ErrInvalidShareInode)
	}
}

func TestShareListsFilterOwnerTargetAndExpiry(t *testing.T) {
	repos := setupShareTestDB(t)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	shares := []Share{
		testShare("active", "owner-1", "target-1", 101),
		testShare("other-owner", "owner-2", "target-1", 102),
		testShare("other-target", "owner-1", "target-2", 103),
		testShare("expired", "owner-1", "target-1", 104),
		testShare("future", "owner-1", "target-1", 105),
	}
	shares[3].ExpiresAt = &past
	shares[4].ExpiresAt = &future
	for index := range shares {
		if err := repos.Shares.Create(&shares[index]); err != nil {
			t.Fatalf("create share %d: %v", index, err)
		}
	}

	owned, err := repos.Shares.ListOwnedByUser("owner-1")
	if err != nil || len(owned) != 3 {
		t.Fatalf("owned shares = %#v, %v", owned, err)
	}
	incoming, err := repos.Shares.ListForUser("target-1")
	if err != nil || len(incoming) != 3 {
		t.Fatalf("incoming shares = %#v, %v", incoming, err)
	}
}

func TestShareListAsFilesAddsOwnerUsername(t *testing.T) {
	repos := setupShareTestDB(t)
	owner, err := repos.Users.Create("owner", "password", "user", "wrapped")
	if err != nil {
		t.Fatal(err)
	}
	target, err := repos.Users.Create("target", "password", "user", "wrapped")
	if err != nil {
		t.Fatal(err)
	}
	share := testShare("view", owner.ID, target.ID, 201)
	if err := repos.Shares.Create(&share); err != nil {
		t.Fatal(err)
	}
	views, err := repos.Shares.ListAsFiles(target.ID)
	if err != nil || len(views) != 1 || views[0].OwnerUsername != owner.Username {
		t.Fatalf("share views = %#v, %v", views, err)
	}
}

func TestShareDeleteAuthorization(t *testing.T) {
	for _, test := range []struct {
		name    string
		actor   string
		deleted bool
	}{
		{name: "owner", actor: "owner-1", deleted: true},
		{name: "target", actor: "target-1", deleted: true},
		{name: "unrelated", actor: "other", deleted: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repos := setupShareTestDB(t)
			share := testShare("delete-"+test.name, "owner-1", "target-1", 301)
			if err := repos.Shares.Create(&share); err != nil {
				t.Fatal(err)
			}
			deleted, err := repos.Shares.Delete(share.ID, test.actor)
			if err != nil || deleted != test.deleted {
				t.Fatalf("delete = %v, %v", deleted, err)
			}
			_, lookupErr := repos.Shares.GetByID(share.ShareID)
			if test.deleted && lookupErr == nil {
				t.Fatal("deleted share remains")
			}
			if !test.deleted && lookupErr != nil {
				t.Fatalf("unrelated actor removed share: %v", lookupErr)
			}
		})
	}
}

func TestShareSyncAndDeleteByInode(t *testing.T) {
	repos := setupShareTestDB(t)
	shares := []Share{
		testShare("match-a", "owner-1", "target-1", 401),
		testShare("match-b", "owner-1", "target-2", 401),
		testShare("other-inode", "owner-1", "target-3", 402),
		testShare("other-owner", "owner-2", "target-4", 401),
	}
	for index := range shares {
		if err := repos.Shares.Create(&shares[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := repos.Shares.SyncByInode("owner-1", 401, "/renamed.md", "renamed.md", 2048, "text/markdown"); err != nil {
		t.Fatal(err)
	}
	for _, shareID := range []string{"match-a", "match-b"} {
		got, err := repos.Shares.GetByID(shareID)
		if err != nil || got.FileName != "renamed.md" || got.FileSize != 2048 || got.ContentType != "text/markdown" {
			t.Fatalf("synced share %s = %#v, %v", shareID, got, err)
		}
	}
	other, err := repos.Shares.GetByID("other-inode")
	if err != nil || other.FileName != "file.txt" {
		t.Fatalf("other inode changed: %#v, %v", other, err)
	}
	if err := repos.Shares.DeleteByInode("owner-1", 401); err != nil {
		t.Fatal(err)
	}
	for _, shareID := range []string{"match-a", "match-b"} {
		if _, err := repos.Shares.GetByID(shareID); err == nil {
			t.Fatalf("share %s survived inode deletion", shareID)
		}
	}
	for _, shareID := range []string{"other-inode", "other-owner"} {
		if _, err := repos.Shares.GetByID(shareID); err != nil {
			t.Fatalf("share %s was deleted: %v", shareID, err)
		}
	}
}

func TestShareUpdateFileSize(t *testing.T) {
	repos := setupShareTestDB(t)
	share := testShare("size", "owner-1", "target-1", 501)
	if err := repos.Shares.Create(&share); err != nil {
		t.Fatal(err)
	}
	if err := repos.Shares.UpdateFileSize(share.ShareID, 4096); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Shares.GetByID(share.ShareID)
	if err != nil || got.FileSize != 4096 {
		t.Fatalf("updated share = %#v, %v", got, err)
	}
}
