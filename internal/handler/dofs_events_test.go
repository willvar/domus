package handler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/dofsbridge"
	"domus/internal/fileview"
	"domus/internal/model"
	"domus/internal/notify"
	"domus/internal/ws"
)

type wakeOnlyMetadata struct {
	dofs.MetadataStore
	reads atomic.Int32
}

func (m *wakeOnlyMetadata) LatestEventSequence(context.Context, string) (int64, error) { return 0, nil }
func (m *wakeOnlyMetadata) ListEvents(context.Context, string, int64, int) ([]dofs.Event, error) {
	m.reads.Add(1)
	return nil, nil
}

func TestDOFSEventLogReadsOnlyAfterWake(t *testing.T) {
	repos := model.NewMemRepos(nil)
	user, err := repos.Users.Create("wake-fixture", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	metadata := &wakeOnlyMetadata{}
	h := &Handler{Repos: repos, Hub: ws.NewHub(), DOFS: &dofsbridge.Runtime{Metadata: metadata}, FileSystem: &fileview.Repo{}}
	if err := h.StartDOFSEventRelay(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(650 * time.Millisecond)
	if metadata.reads.Load() != 0 {
		t.Fatal("idle event relay polled metadata")
	}
	h.publishMediaEvent(notify.Event{Kind: "dofs", UserID: user.ID})
	if metadata.reads.Load() != 1 {
		t.Fatal("change hint did not drain the namespace log")
	}
	h.publishMediaEvent(notify.Event{Kind: "dofs", UserID: "missing-user"})
	if metadata.reads.Load() != 1 {
		t.Fatal("unknown-user hint read another namespace")
	}
}

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
