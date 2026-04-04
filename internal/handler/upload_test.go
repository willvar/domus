package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/middleware"
	"zephyr/internal/model"
)

type uploadInitResult struct {
	UploadID string
	TaskID   string
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
	return uploadInitResult{UploadID: uploadID, TaskID: taskID}
}

func putUploadPartForTest(t *testing.T, app *fiber.App, cookie, uploadID string, partNumber int, chunk []byte) *http.Response {
	t.Helper()

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	_ = writer.WriteField("upload_id", uploadID)
	_ = writer.WriteField("part_number", strconv.Itoa(partNumber))
	if chunk != nil {
		fw, err := writer.CreateFormFile("chunk", "part.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.Close()

	req := httptest.NewRequest("PUT", "/file/upload/part", &payload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func completeUploadForTest(t *testing.T, app *fiber.App, cookie, uploadID string) *http.Response {
	t.Helper()

	body, _ := json.Marshal(map[string]any{
		"upload_id": uploadID,
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

func TestUploadPartRejectsOutOfRangePartNumber(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "a.txt", 1)

	resp := putUploadPartForTest(t, app, cookie, initResult.UploadID, 2, []byte("x"))
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "invalid_part_number" {
		t.Fatalf("expected invalid_part_number, got %v", result["error"])
	}
}

func TestUploadPartRejectsInvalidChunkSize(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "b.txt", 2)

	resp := putUploadPartForTest(t, app, cookie, initResult.UploadID, 1, []byte("x"))
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "invalid_chunk_size" {
		t.Fatalf("expected invalid_chunk_size, got %v", result["error"])
	}
}

func TestUploadPartDeduplicatesCompletedParts(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "c.txt", 1)

	resp1 := putUploadPartForTest(t, app, cookie, initResult.UploadID, 1, []byte("x"))
	if resp1.StatusCode != 200 {
		t.Fatalf("first part upload: expected 200, got %d", resp1.StatusCode)
	}

	resp2 := putUploadPartForTest(t, app, cookie, initResult.UploadID, 1, []byte("x"))
	if resp2.StatusCode != 200 {
		t.Fatalf("second part upload: expected 200, got %d", resp2.StatusCode)
	}

	req := httptest.NewRequest("GET", "/file/upload/?upload_id="+initResult.UploadID, nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	statusResp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if statusResp.StatusCode != 200 {
		t.Fatalf("status: expected 200, got %d", statusResp.StatusCode)
	}

	var status map[string]any
	_ = json.NewDecoder(statusResp.Body).Decode(&status)
	parts, ok := status["parts"].([]any)
	if !ok {
		t.Fatal("status response missing parts array")
	}
	if len(parts) != 1 {
		t.Fatalf("expected exactly 1 completed part, got %d", len(parts))
	}
}

func TestUploadPartRejectsZeroSizeUpload(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "d.txt", 0)

	resp := putUploadPartForTest(t, app, cookie, initResult.UploadID, 1, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "upload_has_no_parts" {
		t.Fatalf("expected upload_has_no_parts, got %v", result["error"])
	}
}

func TestUploadCompleteRejectsIncompleteUpload(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "e.txt", 2)

	resp := completeUploadForTest(t, app, cookie, initResult.UploadID)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["error"] != "upload_incomplete" {
		t.Fatalf("expected upload_incomplete, got %v", result["error"])
	}
}

func TestUploadCompleteAcceptsFullyUploadedFile(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "f.txt", 1)

	partResp := putUploadPartForTest(t, app, cookie, initResult.UploadID, 1, []byte("x"))
	if partResp.StatusCode != 200 {
		t.Fatalf("part upload: expected 200, got %d", partResp.StatusCode)
	}

	completeResp := completeUploadForTest(t, app, cookie, initResult.UploadID)
	if completeResp.StatusCode != 200 {
		t.Fatalf("complete upload: expected 200, got %d", completeResp.StatusCode)
	}
}

func TestUploadStatusReturnsActiveForUploading(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "h.txt", 1)

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

func TestUploadAbortCancelsTask(t *testing.T) {
	app, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	initResult := initUploadForTest(t, app, cookie, "g.txt", 1)

	req := httptest.NewRequest("DELETE", "/file/upload/?upload_id="+url.QueryEscape(initResult.UploadID), nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("abort upload: expected 200, got %d", resp.StatusCode)
	}

	task, err := model.GetTask(initResult.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "cancelled" {
		t.Fatalf("expected cancelled task, got %s", task.Status)
	}
}
