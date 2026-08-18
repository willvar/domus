package handler

import (
	"context"
	"errors"
	"strings"

	"domus/internal/model"

	"gorm.io/gorm"
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
	return h.permanentlyDeletePathWithProgress(userID, resolvedPath, isDir, nil)
}

func (h *Handler) deleteFileData(userID string, record *model.FileRecord) error {
	// DOFS owns immutable generations and defers tombstone collection until the
	// writable mount can prove no open Unix handle still references the inode.
	return nil
}

func (h *Handler) cleanupThumbnailStorage(userID string, source *model.FileRecord) error {
	if source == nil || source.ThumbnailKey == "" {
		return nil
	}
	thumbnail, err := h.Repos.Files.GetByStorageKey(userID, source.ThumbnailKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if thumbnail.ID == source.ID || thumbnail.UserID != userID || thumbnail.StorageKey() != source.ThumbnailKey {
		return errors.New("refusing to delete a mismatched thumbnail record")
	}
	return h.Repos.Files.Delete(userID, thumbnail.Path)
}

func (h *Handler) deleteFileStorage(userID string, record *model.FileRecord) error {
	if err := h.deleteFileData(userID, record); err != nil {
		return err
	}
	return h.cleanupThumbnailStorage(userID, record)
}

func (h *Handler) revokeFileShares(userID string, inode int64) {
	shares, _ := h.Repos.Shares.ListOwnedByUser(userID)
	_ = h.Repos.Shares.DeleteByInode(userID, inode)
	if h.Hub == nil {
		return
	}
	for _, share := range shares {
		if share.FileInode == inode {
			h.Hub.SendToUser(share.TargetUserID, map[string]any{
				"event": "dir.changed",
				"data":  map[string]any{"path": "__shared__/", "change_type": "refresh"},
			})
		}
	}
}

func (h *Handler) permanentlyDeletePathWithProgress(userID, resolvedPath string, isDir bool, progress func(done, total int, current string)) error {
	if isDir {
		records, err := h.Repos.Files.ListByPrefix(userID, resolvedPath)
		if err != nil || len(records) == 0 {
			return nil
		}
		for i := range records {
			if records[i].IsDir {
				continue
			}
			if err := h.cleanupThumbnailStorage(userID, &records[i]); err != nil {
				return err
			}
			if progress != nil {
				progress(i+1, len(records), records[i].Path)
			}
			h.revokeFileShares(userID, records[i].ID)
		}
		_ = h.Repos.Files.DeleteByPrefix(userID, resolvedPath)
		return nil
	}

	rec, err := h.Repos.Files.Get(userID, resolvedPath)
	if err != nil {
		return nil
	}
	if rec.Status != "ready" && rec.OSSUploadID != "" {
		_ = h.Store.AbortMultipartUpload(rec.StorageKey(), rec.OSSUploadID)
		if h.FileSystem != nil {
			_ = h.FileSystem.AbortDirectUpload(context.Background(), userID, rec.UploadID)
		}
		return nil
	}
	if err := h.deleteFileStorage(userID, rec); err != nil {
		return err
	}
	_ = h.Repos.Files.Delete(userID, resolvedPath)
	h.revokeFileShares(userID, rec.ID)
	return nil
}

func shouldMoveShares(srcAppPath, dstAppPath string) bool {
	return !isTrashAppPath(srcAppPath) && !isTrashAppPath(dstAppPath)
}

func shouldDeleteSharesOnMove(srcAppPath, dstAppPath string) bool {
	return !isTrashAppPath(srcAppPath) && isTrashAppPath(dstAppPath)
}

func syncMovedFileRecords(h *Handler, userID, srcResolved, dstResolved, srcAppPath, dstAppPath string, isDir bool) {
	if isDir {
		records, _ := h.Repos.Files.ListByPrefix(userID, srcResolved)
		_ = h.Repos.Files.MoveByPrefix(userID, srcResolved, dstResolved)
		if shouldMoveShares(srcAppPath, dstAppPath) {
			for index := range records {
				if records[index].IsDir {
					continue
				}
				if current, err := h.Repos.Files.GetByID(userID, records[index].ID); err == nil {
					_ = h.Repos.Shares.SyncByInode(
						userID, current.ID, current.Path, current.Name, current.Size, current.ContentType,
					)
				}
			}
		} else if shouldDeleteSharesOnMove(srcAppPath, dstAppPath) {
			for index := range records {
				if !records[index].IsDir {
					h.revokeFileShares(userID, records[index].ID)
				}
			}
		}
		return
	}

	record, _ := h.Repos.Files.Get(userID, srcResolved)
	newName := strings.TrimSuffix(dstAppPath, "/")
	if idx := strings.LastIndex(newName, "/"); idx >= 0 {
		newName = newName[idx+1:]
	}
	_ = h.Repos.Files.Move(userID, srcResolved, dstResolved, newName)
	if shouldMoveShares(srcAppPath, dstAppPath) {
		if record != nil {
			if current, err := h.Repos.Files.GetByID(userID, record.ID); err == nil {
				_ = h.Repos.Shares.SyncByInode(
					userID, current.ID, current.Path, current.Name, current.Size, current.ContentType,
				)
			}
		}
	} else if shouldDeleteSharesOnMove(srcAppPath, dstAppPath) {
		if record != nil {
			h.revokeFileShares(userID, record.ID)
		}
	}
}

func movePathViaStore(h *Handler, userID, srcResolved, dstResolved string, isDir bool, progress func(done, total int, current string)) error {
	if progress != nil {
		progress(1, 1, srcResolved)
	}
	return nil
}
