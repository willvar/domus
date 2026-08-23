package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"domus/internal/middleware"
	"domus/internal/model"
	"gorm.io/gorm"
)

func trashRequest(t *testing.T, app interface {
	Test(*http.Request, ...int) (*http.Response, error)
}, cookie, method, target string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func trashVisiblePath(t *testing.T, app interface {
	Test(*http.Request, ...int) (*http.Response, error)
}, cookie, path string, inode int64) string {
	t.Helper()
	response := trashRequest(t, app, cookie, http.MethodPost, "/trash/", map[string]any{
		"path": path, "expected_inode": inode,
	})
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("trash %s status=%d body=%s", path, response.StatusCode, body)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}

func TestSamePathCanProduceMultipleTrashEntries(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")

	var inodes []int64
	for _, objectKey := range []string{"first", "second"} {
		if err := repositories.Files.Upsert(user.ID, "/same.txt", "same.txt", false, 5, "text/plain", "", model.UpsertFileOpts{ObjectKey: objectKey}); err != nil {
			t.Fatal(err)
		}
		record, err := repositories.Files.Get(user.ID, "/same.txt")
		if err != nil {
			t.Fatal(err)
		}
		inodes = append(inodes, record.ID)
		trashVisiblePath(t, app, cookie, "/same.txt", record.ID)
	}
	if inodes[0] == inodes[1] {
		t.Fatal("recreated path reused the trashed inode")
	}
	entries, err := repositories.Trash.List(user.ID, 100, 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}
	if entries[0].ID == entries[1].ID || entries[0].OriginalPath != "/same.txt" || entries[1].OriginalPath != "/same.txt" {
		t.Fatalf("same-path entries=%#v", entries)
	}
}

func TestTrashNamedRootDirectoryIsOrdinaryUserData(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	response := trashRequest(t, app, cookie, http.MethodPost, "/file/mkdir", map[string]any{"path": "/__trash__/"})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("mkdir /__trash__/ status=%d body=%s", response.StatusCode, body)
	}
	user, _ := repositories.Users.GetByUsername("root")
	if _, err := repositories.Files.Get(user.ID, "/__trash__/"); err != nil {
		t.Fatalf("ordinary /__trash__/ missing: %v", err)
	}
}

func TestTrashEntryIsScopedToAuthenticatedUser(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	ownerCookie := loginAs("owner", "password")
	otherCookie := loginAs("other", "password")
	owner, _ := repositories.Users.GetByUsername("owner")
	if err := repositories.Files.Upsert(owner.ID, "/private/", "private", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	root, _ := repositories.Files.Get(owner.ID, "/private/")
	entryID := trashVisiblePath(t, app, ownerCookie, "/private/", root.ID)

	response := trashRequest(t, app, otherCookie, http.MethodGet, "/trash/"+entryID+"/list", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user trash access status=%d", response.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body["error"] != "trash_not_found" {
		t.Fatalf("cross-user response=%#v err=%v", body, err)
	}
	if _, err := repositories.Trash.Get(owner.ID, entryID); err != nil {
		t.Fatalf("owner entry changed by cross-user request: %v", err)
	}
}

func TestPartialRestoreThenWholeDirectoryRestoreMergesRemainder(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, "/project/", "project", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.txt", "notes.txt"} {
		if err := repositories.Files.Upsert(user.ID, "/project/"+name, name, false, 1, "text/plain", ""); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := repositories.Files.Get(user.ID, "/project/")
	entryID := trashVisiblePath(t, app, cookie, "/project/", root.ID)
	report, err := repositories.Files.Get(user.ID, trashItemsStorageRootPath+entryID+"/report.txt")
	if err != nil {
		t.Fatal(err)
	}

	response := trashRequest(t, app, cookie, http.MethodPost, "/trash/"+entryID+"/restore", map[string]any{
		"path": "/report.txt", "expected_inode": report.ID,
	})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("partial restore status=%d body=%s", response.StatusCode, body)
	}
	if _, err := repositories.Files.Get(user.ID, "/project/report.txt"); err != nil {
		t.Fatalf("partially restored file missing: %v", err)
	}
	if _, err := repositories.Files.Get(user.ID, trashItemsStorageRootPath+entryID+"/notes.txt"); err != nil {
		t.Fatalf("remaining trash content missing: %v", err)
	}

	response = trashRequest(t, app, cookie, http.MethodPost, "/trash/"+entryID+"/restore", map[string]any{
		"path": "/", "expected_inode": root.ID, "conflict": "merge",
	})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("merged restore status=%d body=%s", response.StatusCode, body)
	}
	for _, name := range []string{"report.txt", "notes.txt"} {
		if _, err := repositories.Files.Get(user.ID, "/project/"+name); err != nil {
			t.Fatalf("merged file %s missing: %v", name, err)
		}
	}
	if _, err := repositories.Trash.Get(user.ID, entryID); err == nil {
		t.Fatal("fully restored trash entry remained")
	}
}

func TestPendingTrashRenameRecoversToReady(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	user, _ := repositories.Users.Create("owner", "password", "user", "")
	if err := repositories.Files.Upsert(user.ID, trashItemsStorageRootPath, "items", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Upsert(user.ID, "/recover.txt", "recover.txt", false, 1, "text/plain", ""); err != nil {
		t.Fatal(err)
	}
	record, _ := repositories.Files.Get(user.ID, "/recover.txt")
	entry := &model.TrashEntry{
		ID: "recover", UserID: user.ID, RootInode: record.ID, OriginalPath: record.Path,
		OriginalName: record.Name, State: model.TrashStatePending,
	}
	if err := repositories.Trash.Create(entry); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Move(user.ID, record.Path, trashEntryStoragePath(entry), entry.ID); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Repos: repositories}
	if err := handler.RecoverTrash(true); err != nil {
		t.Fatal(err)
	}
	recovered, err := repositories.Trash.Get(user.ID, entry.ID)
	if err != nil || recovered.State != model.TrashStateReady {
		t.Fatalf("recovered entry=%#v err=%v", recovered, err)
	}
}

func TestTrashChildPermanentDeleteRemovesLastEmptyEntry(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, "/only/", "only", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Upsert(user.ID, "/only/child.txt", "child.txt", false, 1, "text/plain", ""); err != nil {
		t.Fatal(err)
	}
	root, _ := repositories.Files.Get(user.ID, "/only/")
	entryID := trashVisiblePath(t, app, cookie, "/only/", root.ID)
	child, _ := repositories.Files.Get(user.ID, trashItemsStorageRootPath+entryID+"/child.txt")

	missingInode := trashRequest(t, app, cookie, http.MethodDelete, "/trash/"+entryID+"?path=%2Fchild.txt", nil)
	if missingInode.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete without inode status=%d", missingInode.StatusCode)
	}
	response := trashRequest(t, app, cookie, http.MethodDelete,
		"/trash/"+entryID+"?path=%2Fchild.txt&expected_inode="+fmt.Sprint(child.ID), nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("delete child status=%d body=%s", response.StatusCode, body)
	}
	if _, err := repositories.Trash.Get(user.ID, entryID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("empty trash entry remained: %v", err)
	}
	if _, err := repositories.Files.Get(user.ID, trashItemsStorageRootPath); err != nil {
		t.Fatalf("items root was deleted: %v", err)
	}
}

func TestMergeRestoreReportsNestedConflictsAndCanRenameThem(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, "/project/", "project", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"same.txt", "other.txt"} {
		if err := repositories.Files.Upsert(user.ID, "/project/"+name, name, false, 1, "text/plain", ""); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := repositories.Files.Get(user.ID, "/project/")
	entryID := trashVisiblePath(t, app, cookie, "/project/", root.ID)
	if err := repositories.Files.Upsert(user.ID, "/project/", "project", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Files.Upsert(user.ID, "/project/same.txt", "same.txt", false, 2, "text/plain", ""); err != nil {
		t.Fatal(err)
	}

	conflict := trashRequest(t, app, cookie, http.MethodPost, "/trash/"+entryID+"/restore", map[string]any{
		"path": "/", "expected_inode": root.ID, "conflict": "merge",
	})
	if conflict.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(conflict.Body)
		t.Fatalf("nested conflict status=%d body=%s", conflict.StatusCode, body)
	}
	var conflictBody struct {
		Error     string          `json:"error"`
		Conflicts []trashConflict `json:"conflicts"`
	}
	if err := json.NewDecoder(conflict.Body).Decode(&conflictBody); err != nil {
		t.Fatal(err)
	}
	if conflictBody.Error != "restore_conflict" || len(conflictBody.Conflicts) != 1 || conflictBody.Conflicts[0].RelativePath != "/same.txt" {
		t.Fatalf("unexpected conflict response: %#v", conflictBody)
	}

	merged := trashRequest(t, app, cookie, http.MethodPost, "/trash/"+entryID+"/restore", map[string]any{
		"path": "/", "expected_inode": root.ID, "conflict": "merge", "nested_conflict": "rename",
	})
	if merged.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(merged.Body)
		t.Fatalf("merge status=%d body=%s", merged.StatusCode, body)
	}
	for _, path := range []string{"/project/same.txt", "/project/same (1).txt", "/project/other.txt"} {
		if _, err := repositories.Files.Get(user.ID, path); err != nil {
			t.Fatalf("merged path %s missing: %v", path, err)
		}
	}
	if _, err := repositories.Trash.Get(user.ID, entryID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("fully merged entry remained: %v", err)
	}
}

func TestPrepareTrashStorageDeletesLegacyAndOrphanPayloadsOnce(t *testing.T) {
	repositories := model.NewMemRepos(nil)
	user, _ := repositories.Users.Create("owner", "password", "user", "")
	for _, directory := range []string{
		"/.domus/trash/legacy/", trashItemsStorageRootPath,
		trashItemsStorageRootPath + "valid/", trashItemsStorageRootPath + "orphan/",
	} {
		if err := repositories.Files.Upsert(user.ID, directory, path.Base(strings.TrimSuffix(directory, "/")), true, 0, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	validRoot, _ := repositories.Files.Get(user.ID, trashItemsStorageRootPath+"valid/")
	entry := &model.TrashEntry{
		ID: "valid", UserID: user.ID, RootInode: validRoot.ID, OriginalPath: "/valid/",
		OriginalName: "valid", IsDir: true, State: model.TrashStateReady,
	}
	if err := repositories.Trash.Create(entry); err != nil {
		t.Fatal(err)
	}
	itemsRoot, _ := repositories.Files.Get(user.ID, trashItemsStorageRootPath)
	handler := &Handler{Repos: repositories}
	if err := handler.PrepareTrashStorage(); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"/.domus/trash/legacy/", trashItemsStorageRootPath + "orphan/"} {
		if _, err := repositories.Files.Get(user.ID, removed); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("obsolete payload %s remained: %v", removed, err)
		}
	}
	if _, err := repositories.Files.Get(user.ID, trashItemsStorageRootPath+"valid/"); err != nil {
		t.Fatalf("valid payload removed: %v", err)
	}
	if err := handler.PrepareTrashStorage(); err != nil {
		t.Fatal(err)
	}
	itemsRootAfter, err := repositories.Files.Get(user.ID, trashItemsStorageRootPath)
	if err != nil || itemsRootAfter.ID != itemsRoot.ID {
		t.Fatalf("items root churned across restart: before=%#v after=%#v err=%v", itemsRoot, itemsRootAfter, err)
	}
}

func TestEmptyTrashProcessesMoreThanOneRepositoryPage(t *testing.T) {
	app, repositories, loginAs := setupTestApp(t)
	cookie := loginAs("root", "password")
	user, _ := repositories.Users.GetByUsername("root")
	if err := repositories.Files.Upsert(user.ID, trashItemsStorageRootPath, "items", true, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 501; index++ {
		id := fmt.Sprintf("entry-%03d", index)
		storagePath := trashItemsStorageRootPath + id
		if err := repositories.Files.Upsert(user.ID, storagePath, id, false, 1, "text/plain", ""); err != nil {
			t.Fatal(err)
		}
		record, _ := repositories.Files.Get(user.ID, storagePath)
		if err := repositories.Trash.Create(&model.TrashEntry{
			ID: id, UserID: user.ID, RootInode: record.ID, OriginalPath: "/" + id,
			OriginalName: id, State: model.TrashStateReady,
		}); err != nil {
			t.Fatal(err)
		}
	}
	response := trashRequest(t, app, cookie, http.MethodDelete, "/trash/", nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("empty trash status=%d body=%s", response.StatusCode, body)
	}
	entries, err := repositories.Trash.List(user.ID, 500, 0)
	if err != nil || len(entries) != 0 {
		t.Fatalf("trash entries remained=%d err=%v", len(entries), err)
	}
}

func TestNormalizeTrashRelativePathRejectsTraversal(t *testing.T) {
	for _, candidate := range []string{"/../secret", "../secret", `..\secret`, "/safe/../secret", `/safe\..\secret`, "/bad\x00name"} {
		if _, err := normalizeTrashRelativePath(candidate); err == nil {
			t.Fatalf("accepted traversal path %q", candidate)
		}
	}
	for _, candidate := range []string{"/", "/safe/file.txt", "/.../file.txt"} {
		if _, err := normalizeTrashRelativePath(candidate); err != nil {
			t.Fatalf("rejected safe path %q: %v", candidate, err)
		}
	}
}
