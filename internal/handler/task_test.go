package handler

import (
	"testing"

	"domus/internal/model"
	"domus/internal/ws"
)

func TestRootCancelUploadCleansTaskOwnerNamespace(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	root, err := repositories.Users.Create("root", "password", "root", "")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repositories.Users.Create("alice", "password", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	const (
		taskID      = "alice-upload-task"
		uploadID    = "alice-upload"
		ossUploadID = "alice-multipart"
		objectPath  = "/video.mp4"
	)
	if err := repositories.Tasks.Create(owner.ID, taskID, "upload", "video.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.CreateUpload(
		owner.ID, uploadID, taskID, ossUploadID, objectPath, "video.mp4", 1024, "video/mp4", "browser",
	); err != nil {
		t.Fatal(err)
	}
	upload, err := repositories.Files.GetUpload(owner.ID, uploadID)
	if err != nil {
		t.Fatal(err)
	}
	expectedObjectKey := ".dofs/test-objects/" + owner.ID + "/" + uploadID

	var abortedKey, abortedID string
	h := &Handler{
		Repos: repositories,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{
			AbortMultipartUploadFn: func(key, id string) error {
				abortedKey, abortedID = key, id
				return nil
			},
		},
	}
	rootSession := &model.Session{UserID: root.ID, Username: root.Username, Role: root.Role}
	if err := h.cancelTask(rootSession, taskID); err != nil {
		t.Fatalf("cancelTask() error = %v", err)
	}
	if upload.StorageKey() != expectedObjectKey {
		t.Fatalf("test upload object key = %q, want %q", upload.StorageKey(), expectedObjectKey)
	}
	if abortedKey != expectedObjectKey || abortedID != ossUploadID {
		t.Fatalf("aborted multipart = (%q, %q), want (%q, %q)", abortedKey, abortedID, expectedObjectKey, ossUploadID)
	}
	if _, err := repositories.Files.Get(owner.ID, objectPath); err == nil {
		t.Fatal("owner upload record still exists")
	}
	task, err := repositories.Tasks.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "cancelled" {
		t.Fatalf("task status = %q, want cancelled", task.Status)
	}
}
