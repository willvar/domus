package handler

import (
	"context"
	"errors"
	"path"
	"strings"

	"domus/internal/model"

	"gorm.io/gorm"
)

const trashStorageRootPath = "/.domus/trash/"

func normalizeAppPath(rawPath string) string {
	if rawPath == "" {
		return "/"
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}
	directory := strings.HasSuffix(rawPath, "/")
	cleaned := path.Clean(rawPath)
	if directory && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

func isProtectedMutationRoot(path string) bool {
	path = normalizeAppPath(path)
	return path == "/"
}

// ensureParentDirRecords creates any missing ancestor directory records for a destination path.
func (h *Handler) ensureParentDirRecords(userID, storagePath string, isDir bool) error {
	storagePath = normalizeAppPath(storagePath)
	trimmed := strings.Trim(storagePath, "/")
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
		if err := h.Repos.Files.Upsert(userID, current, parts[i], true, 0, "", ""); err != nil {
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(records) == 0 {
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
		if err := h.Repos.Files.DeleteByPrefix(userID, resolvedPath); err != nil {
			return err
		}
		h.reclaimNamespaceBestEffort(userID)
		return nil
	}

	rec, err := h.Repos.Files.Get(userID, resolvedPath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
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
	if err := h.Repos.Files.Delete(userID, resolvedPath); err != nil {
		return err
	}
	h.reclaimNamespaceBestEffort(userID)
	h.revokeFileShares(userID, rec.ID)
	return nil
}

func syncMovedFileRecords(h *Handler, userID, srcResolved, dstResolved, srcAppPath, dstAppPath string, isDir bool) error {
	if isDir {
		records, err := h.Repos.Files.ListByPrefix(userID, srcResolved)
		if err != nil {
			return err
		}
		if err := h.Repos.Files.MoveByPrefix(userID, srcResolved, dstResolved); err != nil {
			return err
		}
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
		return nil
	}

	record, err := h.Repos.Files.Get(userID, srcResolved)
	if err != nil {
		return err
	}
	newName := strings.TrimSuffix(dstAppPath, "/")
	if idx := strings.LastIndex(newName, "/"); idx >= 0 {
		newName = newName[idx+1:]
	}
	if err := h.Repos.Files.Move(userID, srcResolved, dstResolved, newName); err != nil {
		return err
	}
	if current, err := h.Repos.Files.GetByID(userID, record.ID); err == nil {
		_ = h.Repos.Shares.SyncByInode(
			userID, current.ID, current.Path, current.Name, current.Size, current.ContentType,
		)
	}
	return nil
}

func movePathViaStore(h *Handler, userID, srcResolved, dstResolved string, isDir bool, progress func(done, total int, current string)) error {
	if progress != nil {
		progress(1, 1, srcResolved)
	}
	return nil
}
