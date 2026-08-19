package handler

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"domus/config"
	"domus/internal/auth"
	"domus/internal/middleware"
	"domus/internal/store"
)

func TestUploadControlPlaneRejectsPayloadFieldsAndNonJSONBodies(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")

	for _, test := range []struct {
		name        string
		contentType string
		body        any
		status      int
	}{
		{name: "raw payload", contentType: "application/octet-stream", body: []byte("plaintext"), status: http.StatusUnsupportedMediaType},
		{name: "multipart payload", contentType: "multipart/form-data; boundary=x", body: []byte("--x\r\nContent-Disposition: form-data; name=\"file\"; filename=\"note.txt\"\r\nContent-Type: text/plain\r\n\r\nplaintext\r\n--x--\r\n"), status: http.StatusUnsupportedMediaType},
		{name: "JSON content field", contentType: "application/json", body: map[string]any{"path": "/", "file_name": "note.txt", "file_size": 4, "dek": string(bytes.Repeat([]byte("0"), 64)), "content": "body"}, status: http.StatusBadRequest},
		{name: "legacy search text field", contentType: "application/json", body: map[string]any{"upload_id": "upload", "encrypted_size": 5, "search_text": "plaintext"}, status: http.StatusBadRequest},
		{name: "legacy client parts field", contentType: "application/json", body: map[string]any{"upload_id": "upload", "encrypted_size": 5, "parts": []any{}}, status: http.StatusBadRequest},
		{name: "completion cannot carry init metadata", contentType: "application/json", body: map[string]any{"upload_id": "d8f05517-f097-44e9-b196-f6a6af4dbf37", "encrypted_size": 5, "path": "plaintext"}, status: http.StatusBadRequest},
		{name: "init cannot carry completion metadata", contentType: "application/json", body: map[string]any{"path": "/", "file_name": "note.txt", "file_size": 4, "dek": string(bytes.Repeat([]byte("0"), 64)), "content_hash": string(bytes.Repeat([]byte("a"), 64))}, status: http.StatusBadRequest},
		{name: "conflict check cannot carry encryption key", contentType: "application/json", body: map[string]any{"path": "/", "names": []string{"note.txt"}, "dek": string(bytes.Repeat([]byte("0"), 64))}, status: http.StatusBadRequest},
		{name: "retired share capability cannot authorize upload", contentType: "application/json", body: map[string]any{"path": "/", "file_name": "note.txt", "file_size": 4, "dek": string(bytes.Repeat([]byte("0"), 64)), "share_id": "retired-capability"}, status: http.StatusBadRequest},
		{name: "content hash must be SHA-256", contentType: "application/json", body: map[string]any{"upload_id": "d8f05517-f097-44e9-b196-f6a6af4dbf37", "encrypted_size": 5, "content_hash": "plaintext"}, status: http.StatusBadRequest},
		{name: "file name cannot be a path", contentType: "application/json", body: map[string]any{"path": "/", "file_name": "nested/note.txt", "file_size": 4, "dek": string(bytes.Repeat([]byte("0"), 64))}, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			var payload []byte
			switch body := test.body.(type) {
			case []byte:
				payload = body
			default:
				payload, _ = json.Marshal(body)
			}
			req := httptest.NewRequest(http.MethodPost, "/file/upload", bytes.NewReader(payload))
			req.Header.Set("Content-Type", test.contentType)
			req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}

func TestPlaintextDiffUploadRoutesAreGone(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	for _, route := range []string{"/file/content/diff", "/file/shared/share-id/content/diff"} {
		req := httptest.NewRequest(http.MethodPut, route, bytes.NewReader([]byte(`{"edits":[]}`)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", route, response.StatusCode)
		}
	}
}

func TestRetiredProductCapabilitiesAreNotRouted(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	for _, test := range []struct {
		method string
		route  string
		body   string
	}{
		{method: http.MethodPost, route: "/file/transcode", body: `{}`},
		{method: http.MethodPost, route: "/file/share", body: `{}`},
		{method: http.MethodGet, route: "/file/shared"},
		{method: http.MethodGet, route: "/file/shares"},
		{method: http.MethodGet, route: "/workspace/"},
		{method: http.MethodPut, route: "/workspace/", body: `{}`},
		{method: http.MethodDelete, route: "/workspace/"},
	} {
		req := httptest.NewRequest(test.method, test.route, bytes.NewBufferString(test.body))
		if test.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", test.method, test.route, response.StatusCode)
		}
	}
}

func TestFileMutationBoundaryRejectsBodyFieldsOutsideUploadRoute(t *testing.T) {
	app, _, loginAs := setupTestApp(t)
	cookie := loginAs("root", "pass")
	for _, test := range []struct {
		route string
		body  string
	}{
		{route: "/file/mkdir", body: `{"path":"/docs/","content":"plaintext"}`},
		{route: "/file/mkdir", body: `{"path":"/docs/","arbitrary_payload":"plaintext"}`},
		{route: "/file/transcode", body: `{"path":"/video.mp4","profile":"video-720p","data":"plaintext"}`},
		{route: "/file/share", body: `{"path":"/note.txt","target_username":"alice","blob":"plaintext"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, test.route, bytes.NewBufferString(test.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", test.route, response.StatusCode)
		}
	}
}

func TestEncryptedMultipartPartCount(t *testing.T) {
	t.Run("empty file still uses one part", func(t *testing.T) {
		if got := encryptedMultipartPartCount(0, directUploadPartSize); got != 1 {
			t.Fatalf("expected 1 part, got %d", got)
		}
	})

	t.Run("matches simple ceil when boundaries line up", func(t *testing.T) {
		plainSize := int64(1024)
		encryptedSize := encryptedFileSize(plainSize)
		want := int(math.Ceil(float64(encryptedSize) / float64(directUploadPartSize)))
		if got := encryptedMultipartPartCount(plainSize, directUploadPartSize); got != want {
			t.Fatalf("expected %d parts, got %d", want, got)
		}
	})

	t.Run("accounts for chunk alignment slack", func(t *testing.T) {
		const plainSize int64 = 285483787
		const want = 56

		const alignmentTestPartSize = int64(5 * 1024 * 1024)
		naive := int((encryptedFileSize(plainSize) + alignmentTestPartSize - 1) / alignmentTestPartSize)
		if naive != 55 {
			t.Fatalf("expected naive calculation to be 55, got %d", naive)
		}

		if got := encryptedMultipartPartCount(plainSize, alignmentTestPartSize); got != want {
			t.Fatalf("expected %d parts, got %d", want, got)
		}
	})

	t.Run("non-final direct parts meet the S3 minimum", func(t *testing.T) {
		fullEncryptedChunk := int64(auth.NonceSize + auth.DefaultChunkSize + auth.TagSize)
		chunksPerPart := (directUploadPartSize - 5) / fullEncryptedChunk
		firstPartSize := int64(5) + chunksPerPart*fullEncryptedChunk
		if firstPartSize < config.UploadChunkSize {
			t.Fatalf("aligned part is %d bytes, below S3 minimum %d", firstPartSize, config.UploadChunkSize)
		}
	})
}

func TestAuthoritativeCompleteParts(t *testing.T) {
	t.Run("accepts contiguous OSS parts with the expected total size", func(t *testing.T) {
		parts, ok := authoritativeCompleteParts([]store.PartInfo{
			{PartNumber: 1, Size: s3MinimumPartSize, ETag: "etag-one"},
			{PartNumber: 2, Size: 7, ETag: "etag-two"},
		}, 2, s3MinimumPartSize+7)
		if !ok {
			t.Fatal("expected parts to be accepted")
		}
		if len(parts) != 2 || parts[0].PartNumber != 1 || parts[0].ETag != "etag-one" || parts[1].PartNumber != 2 {
			t.Fatalf("unexpected completion parts: %+v", parts)
		}
	})

	for _, tc := range []struct {
		name          string
		parts         []store.PartInfo
		expectedCount int
		expectedSize  int64
	}{
		{name: "too many parts", parts: []store.PartInfo{{PartNumber: 1, Size: s3MinimumPartSize, ETag: "one"}, {PartNumber: 2, Size: 5, ETag: "two"}}, expectedCount: 1, expectedSize: s3MinimumPartSize + 5},
		{name: "non-contiguous part number", parts: []store.PartInfo{{PartNumber: 2, Size: 5, ETag: "etag"}}, expectedCount: 1, expectedSize: 5},
		{name: "missing authoritative etag", parts: []store.PartInfo{{PartNumber: 1, Size: 5}}, expectedCount: 1, expectedSize: 5},
		{name: "wrong total size", parts: []store.PartInfo{{PartNumber: 1, Size: 4, ETag: "etag"}}, expectedCount: 1, expectedSize: 5},
		{name: "empty part", parts: []store.PartInfo{{PartNumber: 1, Size: 0, ETag: "etag"}}, expectedCount: 1, expectedSize: 5},
		{name: "short non-final part", parts: []store.PartInfo{{PartNumber: 1, Size: s3MinimumPartSize - 1, ETag: "one"}, {PartNumber: 2, Size: 5, ETag: "two"}}, expectedCount: 2, expectedSize: s3MinimumPartSize + 4},
		{name: "oversized part", parts: []store.PartInfo{{PartNumber: 1, Size: s3MaximumPartSize + 1, ETag: "etag"}}, expectedCount: 1, expectedSize: s3MaximumPartSize + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := authoritativeCompleteParts(tc.parts, tc.expectedCount, tc.expectedSize); ok {
				t.Fatal("expected parts to be rejected")
			}
		})
	}
}
