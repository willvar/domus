package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"domus/internal/httpx"
)

var fileControlFields = map[string]map[string]struct{}{
	"/file/mkdir":            fieldSet("path"),
	"/file/thumbnail":        fieldSet("path"),
	"/file/rename":           fieldSet("old_path", "new_path", "is_dir"),
	"/file/copy":             fieldSet("src_path", "dst_path", "is_dir"),
	"/file/move":             fieldSet("src_path", "dst_path", "is_dir"),
	"/file/history/restore":  fieldSet("path", "generation", "expected_generation"),
	"/file/upload/heartbeat": fieldSet("upload_id"),
	"/file/upload/cancel":    fieldSet("upload_id", "task_id", "reason", "status"),
	"/file/upload/cleanup":   fieldSet("client_instance_id"),
	"/trash":                 fieldSet("path", "expected_inode"),
}

func fieldSet(names ...string) map[string]struct{} {
	fields := make(map[string]struct{}, len(names))
	for _, name := range names {
		fields[name] = struct{}{}
	}
	return fields
}

// rejectFileContentPayload makes the HTTP control-plane boundary explicit.
// Every supported file mutation carries JSON metadata only; user bytes belong
// on the browser-to-object-store presigned connection.
func rejectFileContentPayload(next http.Handler) http.Handler {
	return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			next.ServeHTTP(w, r)
			return nil
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			return httpx.JSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "json_control_message_required"})
		}
		body, err := httpx.ReadBody(r)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		}

		// Known mutation endpoints are closed schemas. This prevents a client from
		// smuggling bytes under an unrecognised JSON key while the handler silently
		// ignores it. Adding a new control-plane field therefore requires an
		// explicit review here.
		path := strings.TrimSuffix(r.URL.Path, "/")
		allowed := fileControlFields[path]
		if path == "/file/upload" {
			allowed = uploadControlFields
		}
		if strings.HasPrefix(path, "/trash/") && strings.HasSuffix(path, "/history/restore") {
			allowed = fieldSet("path", "generation", "expected_generation")
		} else if strings.HasPrefix(path, "/trash/") && strings.HasSuffix(path, "/restore") {
			allowed = fieldSet("path", "expected_inode", "conflict", "nested_conflict")
		}
		if allowed != nil {
			for name := range fields {
				if _, exists := allowed[name]; !exists {
					return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported_file_control_field"})
				}
			}
			next.ServeHTTP(w, r)
			return nil
		}

		// Unknown routes still reach the 404 handler, but reject conventional
		// payload names first so retired content endpoints cannot reappear by
		// accident with their former request shape.
		for _, name := range []string{
			"content", "plaintext", "file", "blob", "bytes", "data", "search_text",
			"parts", "patch", "diff", "delta", "chunk", "ciphertext", "encrypted_data", "base64",
		} {
			if _, exists := fields[name]; exists {
				return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "file_payload_forbidden"})
			}
		}
		next.ServeHTTP(w, r)
		return nil
	})
}
