package handler

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
)

var fileControlFields = map[string]map[string]struct{}{
	"/file/mkdir":            fieldSet("path"),
	"/file/rename":           fieldSet("old_path", "new_path", "is_dir"),
	"/file/copy":             fieldSet("src_path", "dst_path", "is_dir"),
	"/file/move":             fieldSet("src_path", "dst_path", "is_dir"),
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
func rejectFileContentPayload(c *fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch:
	default:
		return c.Next()
	}
	if !strings.HasPrefix(strings.ToLower(c.Get(fiber.HeaderContentType)), fiber.MIMEApplicationJSON) {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"error": "json_control_message_required"})
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &fields); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_request"})
	}

	// Known mutation endpoints are closed schemas. This prevents a client from
	// smuggling bytes under an unrecognised JSON key while the handler silently
	// ignores it. Adding a new control-plane field therefore requires an
	// explicit review here.
	path := strings.TrimSuffix(c.Path(), "/")
	allowed := fileControlFields[path]
	if path == "/file/upload" {
		allowed = uploadControlFields
	}
	if strings.HasPrefix(path, "/trash/") && strings.HasSuffix(path, "/restore") {
		allowed = fieldSet("path", "expected_inode", "conflict", "nested_conflict")
	}
	if allowed != nil {
		for name := range fields {
			if _, exists := allowed[name]; !exists {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported_file_control_field"})
			}
		}
		return c.Next()
	}

	// Unknown routes still reach Fiber's 404 handler, but reject conventional
	// payload names first so retired content endpoints cannot reappear by
	// accident with their former request shape.
	for _, name := range []string{
		"content", "plaintext", "file", "blob", "bytes", "data", "search_text",
		"parts", "patch", "diff", "delta", "chunk", "ciphertext", "encrypted_data", "base64",
	} {
		if _, exists := fields[name]; exists {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file_payload_forbidden"})
		}
	}
	return c.Next()
}
