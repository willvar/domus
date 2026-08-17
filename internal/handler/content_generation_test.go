package handler

import (
	"bytes"
	"strings"
	"testing"

	"domus/internal/model"
)

func TestPublishPatchedGenerationUsesCAS(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	const (
		userID   = "generation-user"
		filePath = "alice/home/alice/note.txt"
	)
	if err := repositories.Files.Upsert(userID, filePath, "note.txt", false, 3, "text/plain", "old-hash"); err != nil {
		t.Fatal(err)
	}
	record, err := repositories.Files.Get(userID, filePath)
	if err != nil {
		t.Fatal(err)
	}

	var uploadedKey string
	var uploadedData []byte
	var deletedKeys []string
	handler := &Handler{
		Repos: repositories,
		Store: &MockFileStore{
			PutObjectBytesFn: func(key string, data []byte) error {
				uploadedKey = key
				uploadedData = append([]byte(nil), data...)
				return nil
			},
			DeleteObjectFn: func(key string) error {
				deletedKeys = append(deletedKeys, key)
				return nil
			},
		},
	}

	payload := []byte("encrypted-generation")
	if err := handler.publishPatchedGeneration(userID, record, payload, 17); err != nil {
		t.Fatalf("publish generation: %v", err)
	}
	current, err := repositories.Files.Get(userID, filePath)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != record.Generation+1 || current.StorageKey() != uploadedKey {
		t.Fatalf("unexpected committed record: %+v", current)
	}
	if current.LegacyObjectKey != filePath {
		t.Fatalf("legacy object key = %q, want %q", current.LegacyObjectKey, filePath)
	}
	if !strings.HasPrefix(uploadedKey, model.DOFSInodeObjectRoot(userID, record.ID)) {
		t.Fatalf("unexpected generation key %q", uploadedKey)
	}
	if !bytes.Equal(uploadedData, payload) || current.Size != 17 || current.ContentHash != "" {
		t.Fatal("generation payload or metadata was not committed")
	}

	if err := handler.publishPatchedGeneration(userID, record, []byte("stale"), 5); err != errGenerationConflict {
		t.Fatalf("stale publish error = %v, want generation conflict", err)
	}
	if len(deletedKeys) != 1 {
		t.Fatalf("uncommitted generation cleanup = %v", deletedKeys)
	}
}

func TestDeleteGeneratedFileStorageReclaimsRememberedLegacyObject(t *testing.T) {
	const userID = "cleanup-user"
	record := &model.FileRecord{
		ID: 42, UserID: userID, Path: "alice/home/alice/renamed.txt",
		ObjectKey:       model.DOFSGenerationObjectKey(userID, 42, 2, "current"),
		LegacyObjectKey: "alice/home/alice/original.txt",
	}
	var deletedPrefix string
	var deletedObject string
	handler := &Handler{Store: &MockFileStore{
		RecursiveDeleteFn: func(prefix string, _ func(done, total int, current string)) error {
			deletedPrefix = prefix
			return nil
		},
		DeleteObjectFn: func(key string) error {
			deletedObject = key
			return nil
		},
	}}
	if err := handler.deleteFileStorage(userID, record); err != nil {
		t.Fatal(err)
	}
	if deletedPrefix != model.DOFSInodeObjectRoot(userID, record.ID) {
		t.Fatalf("deleted prefix = %q", deletedPrefix)
	}
	if deletedObject != record.LegacyObjectKey {
		t.Fatalf("deleted legacy object = %q", deletedObject)
	}
}

func TestDeleteFileStorageReclaimsLinkedThumbnailRecord(t *testing.T) {
	const (
		userID        = "thumbnail-cleanup-user"
		sourcePath    = "alice/home/alice/photo.jpg"
		thumbnailPath = "alice/home/alice/.user/thumbnails/photo.webp"
	)
	repositories := model.NewMemRepos(nil)
	if err := repositories.Files.Upsert(userID, sourcePath, "photo.jpg", false, 8, "image/jpeg", ""); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Upsert(userID, thumbnailPath, "photo.webp", false, 3, "image/webp", ""); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := repositories.Files.Get(userID, thumbnailPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.UpdateThumbnail(userID, sourcePath, thumbnail.StorageKey(), "wrapped", 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	source, err := repositories.Files.Get(userID, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var deleted []string
	handler := &Handler{Repos: repositories, Store: &MockFileStore{DeleteObjectFn: func(key string) error {
		deleted = append(deleted, key)
		return nil
	}}}
	if err := handler.deleteFileStorage(userID, source); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Files.GetByID(userID, thumbnail.ID); err == nil {
		t.Fatal("thumbnail metadata survived source storage deletion")
	}
	if len(deleted) != 2 || deleted[0] != sourcePath || deleted[1] != thumbnailPath {
		t.Fatalf("deleted objects = %v", deleted)
	}
}

func TestCloneDirectoryRebindsCopiedThumbnail(t *testing.T) {
	const (
		userID    = "clone-thumbnail-user"
		srcPrefix = "alice/home/alice/source/"
		dstPrefix = "alice/home/alice/copy/"
	)
	repositories := model.NewMemRepos(nil)
	sourcePath := srcPrefix + "photo.jpg"
	thumbnailPath := srcPrefix + ".user/derived/photo.preview.jpg"
	thumbnailObject := model.DOFSGenerationObjectKey(userID, 22, 2, "preview")
	if err := repositories.Files.Upsert(userID, sourcePath, "photo.jpg", false, 8, "image/jpeg", ""); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Upsert(
		userID, thumbnailPath, "photo.preview.jpg", false, 3, "image/jpeg", "",
		model.UpsertFileOpts{WrappedDEK: "thumbnail-dek", ObjectKey: thumbnailObject},
	); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.UpdateThumbnail(userID, sourcePath, thumbnailObject, "thumbnail-dek", 10, 5, 0); err != nil {
		t.Fatal(err)
	}
	var copies [][2]string
	handler := &Handler{Repos: repositories, Store: &MockFileStore{CopyObjectFn: func(source, destination string) error {
		copies = append(copies, [2]string{source, destination})
		return nil
	}}}
	if err := handler.cloneDirFiles(userID, srcPrefix, dstPrefix); err != nil {
		t.Fatal(err)
	}
	copiedThumbnailPath := dstPrefix + ".user/derived/photo.preview.jpg"
	copiedSource, err := repositories.Files.Get(userID, dstPrefix+"photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if copiedSource.ThumbnailKey != copiedThumbnailPath || copiedSource.ThumbnailWrappedDEK != "thumbnail-dek" {
		t.Fatalf("copied source thumbnail = %+v", copiedSource)
	}
	copiedThumbnail, err := repositories.Files.Get(userID, copiedThumbnailPath)
	if err != nil || copiedThumbnail.StorageKey() != copiedThumbnailPath {
		t.Fatalf("copied thumbnail = %+v, %v", copiedThumbnail, err)
	}
	if len(copies) != 1 || copies[0] != [2]string{thumbnailObject, copiedThumbnailPath} {
		t.Fatalf("object copies = %v", copies)
	}
}

func TestDeleteDirectoryReclaimsGenerationsAfterCurrentObjectReturnsToLogicalPath(t *testing.T) {
	const (
		userID   = "cleanup-user"
		dirPath  = "alice/home/alice/docs/"
		filePath = dirPath + "note.txt"
	)
	repositories := model.NewMemRepos(nil)
	generated := model.DOFSGenerationObjectKey(userID, 1, 1, "old-generation")
	if err := repositories.Files.Upsert(
		userID, filePath, "note.txt", false, 4, "text/plain", "",
		model.UpsertFileOpts{WrappedDEK: "wrapped", ObjectKey: generated},
	); err != nil {
		t.Fatal(err)
	}
	// Simulate a later browser upload switching the current object back to its
	// logical path while old immutable generations still exist.
	if err := repositories.Files.Upsert(userID, filePath, "note.txt", false, 5, "text/plain", ""); err != nil {
		t.Fatal(err)
	}
	record, err := repositories.Files.Get(userID, filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !record.HasObjectGenerations || record.StorageKey() != filePath {
		t.Fatalf("unexpected mixed storage record: %+v", record)
	}

	var deletedPrefixes []string
	handler := &Handler{Repos: repositories, Store: &MockFileStore{
		RecursiveDeleteFn: func(prefix string, _ func(done, total int, current string)) error {
			deletedPrefixes = append(deletedPrefixes, prefix)
			return nil
		},
	}}
	if err := handler.permanentlyDeletePath(userID, dirPath, true); err != nil {
		t.Fatal(err)
	}
	wantInodeRoot := model.DOFSInodeObjectRoot(userID, record.ID)
	if len(deletedPrefixes) != 2 || deletedPrefixes[0] != dirPath || deletedPrefixes[1] != wantInodeRoot {
		t.Fatalf("deleted prefixes = %v, want [%q %q]", deletedPrefixes, dirPath, wantInodeRoot)
	}
}
