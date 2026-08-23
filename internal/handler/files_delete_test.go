package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"domus/internal/middleware"
)

func TestTrashDirectoryCreatesOpaqueEntryAndMovesStableInode(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, "/projects/", "projects", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	root, err := repositories.Files.Get(user.ID, "/projects/")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"path": "/projects/", "expected_inode": root.ID})
	request := httptest.NewRequest(http.MethodPost, "/trash/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("trash directory status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	entries, err := repositories.Trash.List(user.ID, 100, 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("trash entries = %#v, %v", entries, err)
	}
	if entries[0].OriginalPath != "/projects/" || entries[0].RootInode != root.ID {
		t.Fatalf("trash entry = %#v", entries[0])
	}
	moved, err := repositories.Files.GetByID(user.ID, root.ID)
	if err != nil || moved.Path != trashEntryStoragePath(&entries[0]) {
		t.Fatalf("moved inode = %#v, %v", moved, err)
	}
}

func TestPermanentDeleteMissingPathIsIdempotent(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	request := httptest.NewRequest(http.MethodDelete, "/file/delete?path=%2Fmissing%2F&permanent=true", nil)
	request.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("permanent delete missing path status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestPermanentDeleteRejectsStaleInode(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, "/keep.txt", "keep.txt", false, 1, "text/plain", ""); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/file/delete?path=%2Fkeep.txt&permanent=true&expected_inode=999", nil)
	request.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale delete status = %d, want %d", response.StatusCode, http.StatusConflict)
	}
	if _, err := repositories.Files.Get(user.ID, "/keep.txt"); err != nil {
		t.Fatalf("replacement file was deleted: %v", err)
	}
}

func TestDeleteEndpointsRejectPathsCanonicalizingToRoot(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, "/keep.txt", "keep.txt", false, 1, "text/plain", ""); err != nil {
		t.Fatal(err)
	}
	record, err := repositories.Files.Get(user.ID, "/keep.txt")
	if err != nil {
		t.Fatal(err)
	}

	permanent := httptest.NewRequest(http.MethodDelete,
		"/file/delete?path=%2Fkeep.txt%2F..&permanent=true&expected_inode=1", nil)
	permanent.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	permanentResponse, err := app.Test(permanent)
	if err != nil {
		t.Fatal(err)
	}
	if permanentResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("canonical-root permanent delete status = %d, want %d", permanentResponse.StatusCode, http.StatusBadRequest)
	}

	body, _ := json.Marshal(map[string]any{
		"path": "/keep.txt/..", "expected_inode": record.ID,
	})
	trash := httptest.NewRequest(http.MethodPost, "/trash/", bytes.NewReader(body))
	trash.Header.Set("Content-Type", "application/json")
	trash.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	trashResponse, err := app.Test(trash)
	if err != nil {
		t.Fatal(err)
	}
	if trashResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("canonical-root trash status = %d, want %d", trashResponse.StatusCode, http.StatusBadRequest)
	}
	if _, err := repositories.Files.Get(user.ID, "/keep.txt"); err != nil {
		t.Fatalf("safe file changed by canonical-root requests: %v", err)
	}
}
