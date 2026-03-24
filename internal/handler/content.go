package handler

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"sort"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
)

// Edit represents a single edit operation for diff-based content update.
type Edit struct {
	Offset int64  `json:"offset"`
	Delete int64  `json:"delete"`
	Insert string `json:"insert"`
}

// applyEdits reads from r, applies edits (sorted by offset, non-overlapping),
// and writes the modified content to w.
func applyEdits(r io.Reader, w io.Writer, edits []Edit) error {
	cursor := int64(0)
	buf := make([]byte, 32*1024)
	for _, edit := range edits {
		// Copy unchanged bytes from cursor to edit.Offset
		toCopy := edit.Offset - cursor
		if toCopy > 0 {
			if _, err := io.CopyBuffer(w, io.LimitReader(r, toCopy), buf); err != nil {
				return err
			}
		}
		// Skip deleted bytes
		if edit.Delete > 0 {
			if _, err := io.CopyBuffer(io.Discard, io.LimitReader(r, edit.Delete), buf); err != nil {
				return err
			}
		}
		// Write inserted text
		if edit.Insert != "" {
			if _, err := io.WriteString(w, edit.Insert); err != nil {
				return err
			}
		}
		cursor = edit.Offset + edit.Delete
	}
	// Copy remaining bytes
	if _, err := io.CopyBuffer(w, r, buf); err != nil {
		return err
	}
	return nil
}

// handlePatchContent applies diff-based edits to a file.
// Body: { path, base_size, edits: [{offset, delete, insert}...] }
func (h *Handler) handlePatchContent(c *fiber.Ctx) error {
	var body struct {
		Path     string `json:"path"`
		BaseSize int64  `json:"base_size"`
		Edits    []Edit `json:"edits"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.Path == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}
	if len(body.Edits) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "edits_required"})
	}

	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}

	// Validate base_size against DB record
	var fileRecord model.FileRecord
	if err := h.DB.Where("path = ?", resolvedPath).First(&fileRecord).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file_not_found"})
	}
	if body.BaseSize != fileRecord.Size {
		return c.Status(409).JSON(fiber.Map{"error": "base_size_mismatch", "current_size": fileRecord.Size})
	}

	// Validate edits: sorted by offset, non-overlapping, within bounds
	sort.Slice(body.Edits, func(i, j int) bool {
		return body.Edits[i].Offset < body.Edits[j].Offset
	})
	for i, edit := range body.Edits {
		if edit.Offset < 0 || edit.Delete < 0 {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_edit"})
		}
		if edit.Offset+edit.Delete > body.BaseSize {
			return c.Status(400).JSON(fiber.Map{"error": "edit_out_of_bounds"})
		}
		if i > 0 {
			prev := body.Edits[i-1]
			if edit.Offset < prev.Offset+prev.Delete {
				return c.Status(400).JSON(fiber.Map{"error": "overlapping_edits"})
			}
		}
	}

	// Calculate new plaintext size
	newSize := body.BaseSize
	for _, edit := range body.Edits {
		newSize += int64(len(edit.Insert)) - edit.Delete
	}
	if newSize < 0 {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_result_size"})
	}

	session := c.Locals("session").(*model.Session)
	encKey, err := h.getFileEncryptionKey(session, resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}

	// Read ciphertext from OSS
	reader, err := h.Store.GetObjectContent(resolvedPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "read_file_failed"})
	}

	// Pipeline: decrypt -> apply edits -> encrypt -> buffer
	// Use pipes to chain goroutines.

	// Pipe 1: decrypt -> edit applicator
	decR, decW := io.Pipe()
	// Pipe 2: edit applicator -> encryptor
	editR, editW := io.Pipe()

	var pipelineErr error
	var encryptedBuf bytes.Buffer

	// Goroutine 1: decrypt ciphertext to plaintext
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		err := auth.DecryptStream(encKey, reader, decW)
		_ = reader.Close()
		_ = decW.CloseWithError(err)
	}()

	// Goroutine 2: apply edits
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		err := applyEdits(decR, editW, body.Edits)
		_ = editW.CloseWithError(err)
	}()

	// Goroutine 3: encrypt edited content
	done3 := make(chan struct{})
	go func() {
		defer close(done3)
		pipelineErr = auth.EncryptStream(encKey, editR, &encryptedBuf)
	}()

	<-done1
	<-done2
	<-done3

	if pipelineErr != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("pipeline_failed: %v", pipelineErr)})
	}

	// Upload encrypted content to OSS
	if err := h.Store.PutObjectBytes(resolvedPath, encryptedBuf.Bytes()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "save_file_failed"})
	}

	// Update file record
	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(resolvedPath))
	_ = model.UpsertFile(session.UserID, resolvedPath, fileName, false, newSize, ct, "")

	h.Audit.LogFromCtx(c, "file_write", body.Path, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true, "new_size": newSize})
}
