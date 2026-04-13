package handler

import (
	"bytes"
	"encoding/hex"
	"io"
	"mime"
	"path/filepath"
	"sort"
	"time"

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

type patchError string

func (e patchError) Error() string { return string(e) }

const (
	errInvalidEdit       patchError = "invalid_edit"
	errEditOutOfBounds   patchError = "edit_out_of_bounds"
	errOverlappingEdits  patchError = "overlapping_edits"
	errInvalidResultSize patchError = "invalid_result_size"
	errReadFileFailed    patchError = "read_file_failed"
	errPipelineFailed    patchError = "pipeline_failed"
	errSaveFileFailed    patchError = "save_file_failed"
)

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

func validatePatchEdits(baseSize int64, edits []Edit) (int64, error) {
	sort.Slice(edits, func(i, j int) bool {
		return edits[i].Offset < edits[j].Offset
	})
	for i, edit := range edits {
		if edit.Offset < 0 || edit.Delete < 0 {
			return 0, errInvalidEdit
		}
		if edit.Offset+edit.Delete > baseSize {
			return 0, errEditOutOfBounds
		}
		if i > 0 {
			prev := edits[i-1]
			if edit.Offset < prev.Offset+prev.Delete {
				return 0, errOverlappingEdits
			}
		}
	}

	newSize := baseSize
	for _, edit := range edits {
		newSize += int64(len(edit.Insert)) - edit.Delete
	}
	if newSize < 0 {
		return 0, errInvalidResultSize
	}
	return newSize, nil
}

func (h *Handler) patchEncryptedContent(objectPath, wrappedDEK string, kek []byte, edits []Edit) error {
	wrappedDEKBytes, err := hex.DecodeString(wrappedDEK)
	if err != nil {
		return &wsError{Code: "internal_error"}
	}
	dek, err := auth.UnwrapDEK(kek, wrappedDEKBytes)
	if err != nil {
		return &wsError{Code: "internal_error"}
	}

	reader, err := h.Store.GetObjectContent(objectPath)
	if err != nil {
		return errReadFileFailed
	}

	decR, decW := io.Pipe()
	editR, editW := io.Pipe()
	var pipelineErr error
	var encryptedBuf bytes.Buffer

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		err := auth.DecryptStream(dek, reader, decW)
		_ = reader.Close()
		_ = decW.CloseWithError(err)
	}()

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		err := applyEdits(decR, editW, edits)
		_ = editW.CloseWithError(err)
	}()

	done3 := make(chan struct{})
	go func() {
		defer close(done3)
		pipelineErr = auth.EncryptStream(dek, editR, &encryptedBuf)
	}()

	<-done1
	<-done2
	<-done3

	if pipelineErr != nil {
		return errPipelineFailed
	}
	if err := h.Store.PutObjectBytes(objectPath, encryptedBuf.Bytes()); err != nil {
		return errSaveFileFailed
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
	session := c.Locals("session").(*model.Session)
	fileRecord, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file_not_found"})
	}
	if body.BaseSize != fileRecord.Size {
		return c.Status(409).JSON(fiber.Map{"error": "base_size_mismatch", "current_size": fileRecord.Size})
	}

	newSize, err := validatePatchEdits(body.BaseSize, body.Edits)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	if err := h.patchEncryptedContent(resolvedPath, fileRecord.WrappedDEK, kek, body.Edits); err != nil {
		code := err.Error()
		if wse, ok := err.(*wsError); ok {
			code = wse.Code
		}
		return c.Status(500).JSON(fiber.Map{"error": code})
	}

	// Update file record
	fileName := filepath.Base(resolvedPath)
	ct := mime.TypeByExtension(filepath.Ext(resolvedPath))
	_ = h.Repos.Files.Upsert(session.UserID, resolvedPath, fileName, false, newSize, ct, "")

	// Notify WebSocket subscribers of the parent directory
	if parent := parentDirOf(resolvedPath); parent != "" {
		appPath := toAppPath(parent, session.Username)
		h.Hub.PushDirChanged(parent, appPath, "modified")
	}

	h.Audit.LogFromCtx(c, "file_write", body.Path, "", "success", 0)
	return c.JSON(fiber.Map{"ok": true, "new_size": newSize})
}

// handleSharePatchContent applies diff-based edits to a shared file.
// Body: { base_size, edits: [{offset, delete, insert}...] }
func (h *Handler) handleSharePatchContent(c *fiber.Ctx) error {
	var body struct {
		BaseSize int64  `json:"base_size"`
		Edits    []Edit `json:"edits"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	shareID := c.Params("share_id")
	if shareID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "share_id_required"})
	}
	if len(body.Edits) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "edits_required"})
	}

	session := c.Locals("session").(*model.Session)
	share, err := h.Repos.Shares.GetByID(shareID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "share_not_found"})
	}
	if session.UserID != share.TargetUserID {
		return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
	}
	if share.Permission != "write" {
		return c.Status(403).JSON(fiber.Map{"error": "readonly_share"})
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		return c.Status(403).JSON(fiber.Map{"error": "share_expired"})
	}

	ownerUser, err := h.Repos.Users.GetByID(share.OwnerID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	fileRecord, err := h.Repos.Files.Get(share.OwnerID, share.FilePath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file_not_found"})
	}
	if body.BaseSize != fileRecord.Size {
		return c.Status(409).JSON(fiber.Map{"error": "base_size_mismatch", "current_size": fileRecord.Size})
	}

	newSize, err := validatePatchEdits(body.BaseSize, body.Edits)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	ownerKEK, err := h.loadUserKEK(share.OwnerID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	if err := h.patchEncryptedContent(share.FilePath, fileRecord.WrappedDEK, ownerKEK, body.Edits); err != nil {
		code := err.Error()
		if wse, ok := err.(*wsError); ok {
			code = wse.Code
		}
		return c.Status(500).JSON(fiber.Map{"error": code})
	}

	fileName := filepath.Base(share.FilePath)
	ct := mime.TypeByExtension(filepath.Ext(share.FilePath))
	_ = h.Repos.Files.Upsert(share.OwnerID, share.FilePath, fileName, false, newSize, ct, "")

	share.FileSize = newSize
	_ = h.Repos.Shares.UpdateFileSize(share.ShareID, newSize)

	h.Audit.LogFromCtx(c, "file_write", share.FilePath, "shared", "success", 0)
	h.notifyParentDir(ownerUser.Username, share.FilePath)

	return c.JSON(fiber.Map{"ok": true, "new_size": newSize})
}
