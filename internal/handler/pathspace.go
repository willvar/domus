package handler

import (
	"strings"

	"zephyr/internal/model"
)

const trashRootPath = "/__trash__/"

func normalizeAppPath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" && strings.HasSuffix(path, "/") {
		return path
	}
	return path
}

func isTrashAppPath(path string) bool {
	path = normalizeAppPath(path)
	return path == trashRootPath || strings.HasPrefix(path, trashRootPath)
}

func trashAppPath(path string) string {
	path = normalizeAppPath(path)
	if path == "/" {
		return trashRootPath
	}
	return trashRootPath + strings.TrimPrefix(path, "/")
}

func restoreAppPath(path string) string {
	path = normalizeAppPath(path)
	if !isTrashAppPath(path) {
		return path
	}
	restored := "/" + strings.TrimPrefix(path, trashRootPath)
	if restored == "//" {
		return "/"
	}
	if strings.HasSuffix(path, "/") && restored != "/" && !strings.HasSuffix(restored, "/") {
		restored += "/"
	}
	return restored
}

// ensureParentDirRecords creates any missing ancestor directory records for a destination path.
// Trash root itself is implicit and intentionally not materialized as a normal file record.
func (h *Handler) ensureParentDirRecords(userID, username, appPath string, isDir bool) error {
	path := normalizeAppPath(appPath)
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, "/")
	end := len(parts) - 1
	if end <= 0 {
		return nil
	}

	current := "/"
	for i := 0; i < end; i++ {
		current += parts[i] + "/"
		if i == 0 && parts[i] == "__trash__" {
			continue
		}
		resolved, err := resolvePath(username, current)
		if err != nil {
			return err
		}
		if err := h.Repos.Files.Upsert(userID, resolved, parts[i], true, 0, "", ""); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) permanentlyDeletePath(userID, resolvedPath string, isDir bool) error {
	if isDir {
		records, err := h.Repos.Files.ListByPrefix(userID, resolvedPath)
		if err != nil || len(records) == 0 {
			return nil
		}
		if err := h.Store.RecursiveDelete(resolvedPath, nil); err != nil {
			return err
		}
		_ = h.Repos.Files.DeleteByPrefix(userID, resolvedPath)
		_ = h.Repos.Shares.DeleteByPrefix(userID, resolvedPath)
		return nil
	}

	rec, err := h.Repos.Files.Get(userID, resolvedPath)
	if err != nil {
		return nil
	}
	if rec.Status != "ready" && rec.OSSUploadID != "" {
		_ = h.Store.AbortMultipartUpload(resolvedPath, rec.OSSUploadID)
	}
	if err := h.Store.DeleteObject(resolvedPath); err != nil {
		return err
	}
	_ = h.Repos.Files.Delete(userID, resolvedPath)
	_ = h.Repos.Shares.DeleteByPath(userID, resolvedPath)
	return nil
}

func shouldMoveShares(srcAppPath, dstAppPath string) bool {
	return !isTrashAppPath(srcAppPath) && !isTrashAppPath(dstAppPath)
}

func shouldDeleteSharesOnMove(srcAppPath, dstAppPath string) bool {
	return !isTrashAppPath(srcAppPath) && isTrashAppPath(dstAppPath)
}

func sourceInTrashWithoutShares(srcAppPath string) bool {
	return isTrashAppPath(srcAppPath)
}

func syncMovedFileRecords(h *Handler, userID, srcResolved, dstResolved, srcAppPath, dstAppPath string, isDir bool) {
	if isDir {
		_ = h.Repos.Files.MoveByPrefix(userID, srcResolved, dstResolved)
		if shouldMoveShares(srcAppPath, dstAppPath) {
			_ = h.Repos.Shares.MoveByPrefix(userID, srcResolved, dstResolved)
		} else if shouldDeleteSharesOnMove(srcAppPath, dstAppPath) {
			_ = h.Repos.Shares.DeleteByPrefix(userID, srcResolved)
		}
		return
	}

	newName := strings.TrimSuffix(dstAppPath, "/")
	if idx := strings.LastIndex(newName, "/"); idx >= 0 {
		newName = newName[idx+1:]
	}
	_ = h.Repos.Files.Move(userID, srcResolved, dstResolved, newName)
	if shouldMoveShares(srcAppPath, dstAppPath) {
		_ = h.Repos.Shares.MoveByPath(userID, srcResolved, dstResolved)
	} else if shouldDeleteSharesOnMove(srcAppPath, dstAppPath) {
		_ = h.Repos.Shares.DeleteByPath(userID, srcResolved)
	}
}

func movePathViaStore(h *Handler, srcResolved, dstResolved string, isDir bool, progress func(done, total int, current string)) error {
	if isDir {
		return h.Store.RecursiveMove(srcResolved, dstResolved, progress)
	}
	err := h.Store.MoveObject(srcResolved, dstResolved)
	if err == nil && progress != nil {
		progress(1, 1, srcResolved)
	}
	return err
}

func existingEntryByPath(records []model.FileRecord, path string) *model.FileRecord {
	for _, record := range records {
		if record.Path == path {
			copied := record
			return &copied
		}
	}
	return nil
}
