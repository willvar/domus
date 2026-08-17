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
		objectPath  = "alice/home/alice/video.mp4"
	)
	if err := repositories.Tasks.Create(owner.ID, taskID, "upload", "video.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.CreateUpload(
		owner.ID, uploadID, taskID, ossUploadID, objectPath, "video.mp4", 1024, "browser",
	); err != nil {
		t.Fatal(err)
	}

	var abortedKey, abortedID, deletedKey string
	h := &Handler{
		Repos: repositories,
		Hub:   ws.NewHub(),
		Store: &MockFileStore{
			AbortMultipartUploadFn: func(key, id string) error {
				abortedKey, abortedID = key, id
				return nil
			},
			DeleteObjectFn: func(key string) error {
				deletedKey = key
				return nil
			},
		},
	}
	rootSession := &model.Session{UserID: root.ID, Username: root.Username, Role: root.Role}
	if err := h.cancelTask(rootSession, taskID); err != nil {
		t.Fatalf("cancelTask() error = %v", err)
	}
	if abortedKey != objectPath || abortedID != ossUploadID {
		t.Fatalf("aborted multipart = (%q, %q), want (%q, %q)", abortedKey, abortedID, objectPath, ossUploadID)
	}
	if deletedKey != objectPath {
		t.Fatalf("deleted object = %q, want %q", deletedKey, objectPath)
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
