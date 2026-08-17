package handler

import (
	"errors"
	"fmt"
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
	if record == nil {
		return nil
	}
	inodeRoot := model.DOFSInodeObjectRoot(userID, record.ID)
	if record.HasObjectGenerations || strings.HasPrefix(record.StorageKey(), inodeRoot) {
		if err := h.Store.RecursiveDelete(inodeRoot, nil); err != nil {
			return err
		}
	}
	exactKeys := []string{record.StorageKey(), record.LegacyObjectKey}
	if strings.HasPrefix(record.StorageKey(), inodeRoot) && record.LegacyObjectKey == "" {
		exactKeys = append(exactKeys, record.Path)
	}
	deleted := make(map[string]struct{}, len(exactKeys))
	for _, key := range exactKeys {
		if key == "" || strings.HasPrefix(key, inodeRoot) {
			continue
		}
		if _, duplicate := deleted[key]; duplicate {
			continue
		}
		if err := h.Store.DeleteObject(key); err != nil {
			return err
		}
		deleted[key] = struct{}{}
	}
	return nil
}

func (h *Handler) cleanupThumbnailStorage(userID string, source *model.FileRecord) error {
	if source == nil || source.ThumbnailKey == "" {
		return nil
	}
	thumbnail, err := h.Repos.Files.GetByStorageKey(userID, source.ThumbnailKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Pre-linkage metadata may refer directly to an object without a file row.
		// Only remove keys proven to stay inside this user's namespace.
		rootComponent := strings.SplitN(source.Path, "/", 2)[0]
		ownedPathKey := rootComponent != "" && rootComponent != ".dofs" && strings.HasPrefix(source.ThumbnailKey, rootComponent+"/")
		if ownedPathKey || strings.HasPrefix(source.ThumbnailKey, model.DOFSObjectRoot(userID)) {
			return h.Store.DeleteObject(source.ThumbnailKey)
		}
		return errors.New("refusing to delete an out-of-scope thumbnail object")
	}
	if err != nil {
		return err
	}
	if thumbnail.ID == source.ID || thumbnail.UserID != userID || thumbnail.StorageKey() != source.ThumbnailKey {
		return errors.New("refusing to delete a mismatched thumbnail record")
	}
	if err := h.deleteFileData(userID, thumbnail); err != nil {
		return fmt.Errorf("delete thumbnail data: %w", err)
	}
	if thumbnail.Status == "deleted" {
		purged, err := h.Repos.Files.PurgeDeletedDOFSNode(userID, thumbnail.ID)
		if err != nil {
			return err
		}
		if !purged {
			return errors.New("retired thumbnail changed before metadata purge")
		}
		return nil
	}
	return h.Repos.Files.Delete(userID, thumbnail.Path)
}

func (h *Handler) deleteFileStorage(userID string, record *model.FileRecord) error {
	if err := h.deleteFileData(userID, record); err != nil {
		return err
	}
	return h.cleanupThumbnailStorage(userID, record)
}

func (h *Handler) permanentlyDeletePathWithProgress(userID, resolvedPath string, isDir bool, progress func(done, total int, current string)) error {
	if isDir {
		records, err := h.Repos.Files.ListByPrefix(userID, resolvedPath)
		if err != nil || len(records) == 0 {
			return nil
		}
		if err := h.Store.RecursiveDelete(resolvedPath, progress); err != nil {
			return err
		}
		for i := range records {
			storageKey := records[i].StorageKey()
			if records[i].IsDir {
				if !strings.HasPrefix(storageKey, resolvedPath) {
					if err := h.Store.DeleteObject(storageKey); err != nil {
						return err
					}
				}
				continue
			}
			if records[i].HasObjectGenerations || !strings.HasPrefix(storageKey, resolvedPath) {
				if err := h.deleteFileStorage(userID, &records[i]); err != nil {
					return err
				}
			}
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
	if err := h.deleteFileStorage(userID, rec); err != nil {
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

func movePathViaStore(h *Handler, userID, srcResolved, dstResolved string, isDir bool, progress func(done, total int, current string)) error {
	if isDir {
		return h.Store.RecursiveMove(srcResolved, dstResolved, progress)
	}
	record, err := h.Repos.Files.Get(userID, srcResolved)
	if err != nil {
		return err
	}
	if record.StorageKey() == srcResolved {
		err = h.Store.MoveObject(srcResolved, dstResolved)
	}
	if err == nil && progress != nil {
		progress(1, 1, srcResolved)
	}
	return err
}
