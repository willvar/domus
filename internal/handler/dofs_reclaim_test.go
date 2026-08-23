package handler

import (
	"bytes"
	"errors"
	"testing"

	"github.com/willvar/dofs"
	metadatamemory "github.com/willvar/dofs/metadata/memory"
	objectmemory "github.com/willvar/dofs/object/memory"

	"domus/internal/dofsbridge"
	"domus/internal/model"
)

func TestDOFSReclaimerDeletesOrphanNamespaceAndObjects(t *testing.T) {
	metadata := metadatamemory.New()
	objects := objectmemory.New()
	repositories := model.NewMemRepos(nil)
	liveUser, err := repositories.Users.Create("live", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, namespaceID := range []string{"orphan", liveUser.ID} {
		if _, err := metadata.CreateNamespace(t.Context(), dofs.Namespace{
			ID: namespaceID, WrappedKEK: bytes.Repeat([]byte{1}, 32),
		}); err != nil {
			t.Fatal(err)
		}
		root, err := dofs.ObjectRoot(namespaceID)
		if err != nil {
			t.Fatal(err)
		}
		if err := objects.Put(t.Context(), root+"fixture", bytes.NewReader([]byte("ciphertext")), 10); err != nil {
			t.Fatal(err)
		}
	}

	handler := &Handler{
		Repos: repositories,
		DOFS:  &dofsbridge.Runtime{Metadata: metadata, Objects: objects},
	}
	handler.reclaimAllNamespaces(t.Context())

	if _, err := metadata.GetNamespace(t.Context(), "orphan"); !errors.Is(err, dofs.ErrNotFound) {
		t.Fatalf("orphan namespace remained: %v", err)
	}
	orphanRoot, _ := dofs.ObjectRoot("orphan")
	if _, exists := objects.Bytes(orphanRoot + "fixture"); exists {
		t.Fatal("orphan namespace object remained")
	}
	if _, err := metadata.GetNamespace(t.Context(), liveUser.ID); err != nil {
		t.Fatalf("live namespace was deleted: %v", err)
	}
	liveRoot, _ := dofs.ObjectRoot(liveUser.ID)
	if _, exists := objects.Bytes(liveRoot + "fixture"); !exists {
		t.Fatal("live namespace object was deleted")
	}
}

func TestFinishTrashMutationRemovesEmptyEntryButPreservesItemsRoot(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	user, err := repositories.Users.Create("owner", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Upsert(user.ID, trashItemsStorageRootPath, "items", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	entry := &model.TrashEntry{
		ID: "entry", UserID: user.ID, RootInode: 2, OriginalPath: "/project/",
		OriginalName: "project", IsDir: true, State: model.TrashStateReady,
	}
	entryPath := trashItemsStorageRootPath + entry.ID + "/"
	if err := repositories.Files.Upsert(user.ID, entryPath, entry.ID, true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	root, err := repositories.Files.Get(user.ID, entryPath)
	if err != nil {
		t.Fatal(err)
	}
	entry.RootInode = root.ID
	if err := repositories.Trash.Create(entry); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Trash.Transition(user.ID, entry.ID, model.TrashStateReady, model.TrashStateRestoring, root.ID, "/child.txt", "/project/child.txt"); err != nil {
		t.Fatal(err)
	}
	entry.OperationPath = "/child.txt"

	handler := &Handler{Repos: repositories}
	if err := handler.finishTrashMutation(user.ID, entry, model.TrashStateRestoring); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Trash.Get(user.ID, entry.ID); err == nil {
		t.Fatal("empty trash entry remained")
	}
	if _, err := repositories.Files.Get(user.ID, trashItemsStorageRootPath); err != nil {
		t.Fatalf("trash items root was deleted: %v", err)
	}
}
