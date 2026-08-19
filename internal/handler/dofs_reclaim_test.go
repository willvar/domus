package handler

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/willvar/dofs"
	metadatamemory "github.com/willvar/dofs/metadata/memory"
	objectmemory "github.com/willvar/dofs/object/memory"
	"gorm.io/gorm"

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

func TestPruneEmptyTrashParentsPreservesTrashRootAndNonEmptyAncestors(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	user, err := repositories.Users.Create("owner", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		"/.domus/", "/.domus/trash/", "/.domus/trash/project/", "/.domus/trash/project/empty/",
	} {
		name := directory[:len(directory)-1]
		name = name[strings.LastIndex(name, "/")+1:]
		if err := repositories.Files.Upsert(user.ID, directory, name, true, 0, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := repositories.Files.Upsert(user.ID, "/.domus/trash/project/keep.txt", "keep.txt", false, 1, "text/plain", ""); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{Repos: repositories}
	if err := handler.pruneEmptyTrashParents(user.ID, "/.domus/trash/project/empty/restored.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Files.Get(user.ID, "/.domus/trash/project/empty/"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("empty trash directory remained: %v", err)
	}
	if _, err := repositories.Files.Get(user.ID, "/.domus/trash/project/"); err != nil {
		t.Fatalf("non-empty trash ancestor was deleted: %v", err)
	}
	if _, err := repositories.Files.Get(user.ID, trashStorageRootPath); err != nil {
		t.Fatalf("trash root was deleted: %v", err)
	}
}
