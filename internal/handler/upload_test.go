package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/middleware"
)

type uploadInitResult struct {
	UploadID    string
	TaskID      string
	OSSUploadID string
	TotalParts  int
}

func initUploadForTest(t *testing.T, app *fiber.App, cookie, fileName string, fileSize int64) uploadInitResult {
	t.Helper()

	body, _ := json.Marshal(map[string]any{
		"path":      "/home/root/",
		"file_name": fileName,
		"file_size": fileSize,
	})
	req := httptest.NewRequest("POST", "/file/upload/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("upload init: expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	uploadID, _ := result["upload_id"].(string)
	if uploadID == "" {
		t.Fatal("upload init: missing upload_id")
	}
	taskID, _ := result["task_id"].(string)
	if taskID == "" {
		t.Fatal("upload init: missing task_id")
	}
	ossUploadID, _ := result["oss_upload_id"].(string)
	totalParts := 0
	if tp, ok := result["total_parts"].(float64); ok {
		totalParts = int(tp)
	}
	return uploadInitResult{UploadID: uploadID, TaskID: taskID, OSSUploadID: ossUploadID, TotalParts: totalParts}
}

func completeUploadForTest(t *testing.T, app *fiber.App, cookie, uploadID, dek, contentHash string, encryptedSize int64, parts []map[string]any) *http.Response {
	t.Helper()

	body, _ := json.Marshal(map[string]any{
		"upload_id":      uploadID,
		"dek":            dek,
		"content_hash":   contentHash,
		"encrypted_size": encryptedSize,
		"parts":          parts,
	})
	req := httptest.NewRequest("POST", "/file/upload/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestUploadInitReturnsMultipartInfo(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	result := initUploadForTest(t, app, cookie, "test.txt", 1024)

	if result.OSSUploadID == "" {
		t.Fatal("expected oss_upload_id")
	}
	if result.TotalParts < 1 {
		t.Fatal("expected at least 1 part")
	}
}

func TestUploadPresignReturnsBatch(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "presign.txt", 1024)

	req := httptest.NewRequest("GET", "/file/upload/presign?upload_id="+url.QueryEscape(initResult.UploadID)+"&start=1&count=3", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("presign: expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	parts, ok := result["parts"].([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("expected 3 presigned URLs, got %v", result["parts"])
	}
}

func TestUploadCompleteFinalizesRecord(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "complete.txt", 10)

	// Use a valid 32-byte DEK hex (64 chars)
	dek := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	encryptedSize := int64(5 + 10 + 28)
	resp := completeUploadForTest(t, app, cookie, initResult.UploadID, dek, hash, encryptedSize, []map[string]any{{"part_number": 1, "etag": "\"mock-etag\""}})
	if resp.StatusCode != 200 {
		var errResult map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errResult)
		t.Fatalf("complete: expected 200, got %d: %v", resp.StatusCode, errResult)
	}
}

func TestUploadStatusReturnsActiveForUploading(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "status.txt", 1)

	req := httptest.NewRequest("GET", "/file/upload/?upload_id="+url.QueryEscape(initResult.UploadID), nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "active" {
		t.Fatalf("expected status active, got %v", result["status"])
	}
	if result["task_id"] != initResult.TaskID {
		t.Fatalf("expected task_id %s, got %v", initResult.TaskID, result["task_id"])
	}
}

func TestInternalUploadInitSkipsTaskCreation(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	body, _ := json.Marshal(map[string]any{
		"path":      "/home/root/.user/thumbnails/",
		"file_name": "thumb.webp",
		"file_size": 128,
		"internal":  true,
	})
	req := httptest.NewRequest("POST", "/file/upload/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("internal upload init: expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	taskID, _ := result["task_id"].(string)
	if taskID != "" {
		t.Fatalf("expected no task_id for internal upload, got %q", taskID)
	}
}

func TestUploadAbortCancelsTask(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "abort.txt", 1)

	body, _ := json.Marshal(map[string]any{"upload_id": initResult.UploadID})
	req := httptest.NewRequest("POST", "/file/upload/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("abort upload: expected 200, got %d", resp.StatusCode)
	}

	task, err := repos.Tasks.Get(initResult.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "cancelled" {
		t.Fatalf("expected cancelled task, got %s", task.Status)
	}
}

func TestUploadAbortCanMarkTaskFailed(t *testing.T) {
	app, repos, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "abort-failed.txt", 1)

	body, _ := json.Marshal(map[string]any{"upload_id": initResult.UploadID, "reason": "upload_failed", "status": "failed"})
	req := httptest.NewRequest("POST", "/file/upload/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("abort upload failed-status: expected 200, got %d", resp.StatusCode)
	}

	task, err := repos.Tasks.Get(initResult.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "failed" {
		t.Fatalf("expected failed task, got %s", task.Status)
	}
}

func TestUploadCompleteRejectsMissingEncryptedSize(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "missing-size.txt", 10)
	resp := completeUploadForTest(t, app, cookie, initResult.UploadID,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		0,
		[]map[string]any{{"part_number": 1, "etag": "\"mock-etag\""}},
	)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestUploadCompleteRejectsInvalidParts(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "invalid-parts.txt", 10)
	resp := completeUploadForTest(t, app, cookie, initResult.UploadID,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		43,
		[]map[string]any{{"part_number": 2, "etag": "\"mock-etag\""}},
	)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
