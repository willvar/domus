package handler

import (
	"errors"
	"testing"

	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/model"
	"domus/internal/ws"
)

func TestDOFSEventKeepsSharesBoundToStableInode(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	owner, err := repositories.Users.Create("owner", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := repositories.Users.Create("target", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	oldPath := "/note.txt"
	newPath := "/renamed.txt"
	if err := repositories.Files.Upsert(owner.ID, oldPath, "note.txt", false, 12, "text/plain", ""); err != nil {
		t.Fatal(err)
	}
	file, err := repositories.Files.Get(owner.ID, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	share := &model.Share{
		ShareID: "stable-share", OwnerID: owner.ID, TargetUserID: target.ID,
		FileInode: file.ID, FilePath: oldPath, FileName: file.Name, FileSize: file.Size,
		ContentType: file.ContentType, WrappedDEK: "wrapped", Permission: "read",
	}
	if err := repositories.Shares.Create(share); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Move(owner.ID, oldPath, newPath, "renamed.txt"); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{Repos: repositories, Hub: ws.NewHub()}
	handler.publishDOFSEvent(owner, dofs.Event{Kind: dofs.EventRename, Inode: uint64(file.ID)})
	updated, err := repositories.Shares.GetByID(share.ShareID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FileInode != file.ID || updated.FilePath != newPath || updated.FileName != "renamed.txt" {
		t.Fatalf("share did not follow inode rename: %#v", updated)
	}

	handler.publishDOFSEvent(owner, dofs.Event{Kind: dofs.EventRemove, Inode: uint64(file.ID)})
	if _, err := repositories.Shares.GetByID(share.ShareID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("share survived inode removal: %v", err)
	}
}

func TestDOFSEventToleratesMissingProjection(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	owner, err := repositories.Users.Create("owner", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	repositories.Files = &model.MockFileRepo{
		GetByIDFn: func(userID string, id int64) (*model.FileRecord, error) {
			// A repository must not return this pair, but the asynchronous relay
			// remains defensive so one malformed projection cannot crash Domus.
			return nil, nil
		},
	}
	handler := &Handler{Repos: repositories, Hub: ws.NewHub()}
	handler.publishDOFSEvent(owner, dofs.Event{Kind: dofs.EventWrite, Inode: 42})
}
