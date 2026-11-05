package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/store"
)

const (
	trashItemsStorageRootPath = "/.domus/trash/items/"
	trashRecoveryGrace        = 2 * time.Minute
)

var errTrashRootInodeMismatch = errors.New("trash root inode mismatch")

type trashFileInfo struct {
	store.FileInfo
	TrashID      string    `json:"trash_id"`
	RelativePath string    `json:"relative_path"`
	OriginalPath string    `json:"original_path"`
	DeletedAt    time.Time `json:"deleted_at"`
}

type trashRestoreRequest struct {
	Path           string `json:"path"`
	ExpectedInode  int64  `json:"expected_inode"`
	Conflict       string `json:"conflict"`
	NestedConflict string `json:"nested_conflict"`
}

func trashEntryStoragePath(entry *model.TrashEntry) string {
	storagePath := trashItemsStorageRootPath + entry.ID
	if entry.IsDir {
		storagePath += "/"
	}
	return storagePath
}

func normalizeTrashRelativePath(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", errors.New("invalid relative path")
	}
	for _, segment := range strings.Split(strings.ReplaceAll(raw, "\\", "/"), "/") {
		if segment == ".." {
			return "", errors.New("invalid relative path")
		}
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	directory := strings.HasSuffix(raw, "/")
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == "" {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		return "", errors.New("invalid relative path")
	}
	if directory && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned, nil
}

func trashStoragePath(entry *model.TrashEntry, relativePath string) (string, error) {
	relativePath, err := normalizeTrashRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	root := trashEntryStoragePath(entry)
	if relativePath == "/" {
		return root, nil
	}
	if !entry.IsDir {
		return "", errors.New("file trash entry has no children")
	}
	return root + strings.TrimPrefix(relativePath, "/"), nil
}

func trashRelativePath(entry *model.TrashEntry, storagePath string) (string, error) {
	root := trashEntryStoragePath(entry)
	if storagePath == root || strings.TrimSuffix(storagePath, "/") == strings.TrimSuffix(root, "/") {
		return "/", nil
	}
	if !entry.IsDir || !strings.HasPrefix(storagePath, root) {
		return "", errors.New("path is outside trash entry")
	}
	relative := "/" + strings.TrimPrefix(storagePath, root)
	if strings.HasSuffix(storagePath, "/") && !strings.HasSuffix(relative, "/") {
		relative += "/"
	}
	return relative, nil
}

func trashOriginalPath(entry *model.TrashEntry, relativePath string, isDir bool) (string, error) {
	relativePath, err := normalizeTrashRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	if relativePath == "/" {
		return entry.OriginalPath, nil
	}
	if !entry.IsDir {
		return "", errors.New("file trash entry has no children")
	}
	target := strings.TrimSuffix(entry.OriginalPath, "/") + "/" + strings.TrimPrefix(relativePath, "/")
	if isDir && !strings.HasSuffix(target, "/") {
		target += "/"
	}
	return target, nil
}

func validateTrashConflict(value string, allowMerge bool) bool {
	switch value {
	case "", "skip", "replace", "rename":
		return true
	case "merge":
		return allowMerge
	default:
		return false
	}
}

func (h *Handler) ensureTrashStorage(userID string) error {
	return h.Repos.Files.Upsert(userID, trashItemsStorageRootPath, "items", true, 0, "", "")
}

func (h *Handler) trashRecord(userID string, entry *model.TrashEntry, relativePath string) (*model.FileRecord, string, error) {
	storagePath, err := trashStoragePath(entry, relativePath)
	if err != nil {
		return nil, "", err
	}
	record, err := h.Repos.Files.Get(userID, storagePath)
	if err != nil {
		return nil, "", err
	}
	relativePath, err = trashRelativePath(entry, record.Path)
	if err != nil {
		return nil, "", err
	}
	if relativePath == "/" && record.ID != entry.RootInode {
		return nil, "", errTrashRootInodeMismatch
	}
	return record, relativePath, nil
}

func (h *Handler) trashFileProjection(entry *model.TrashEntry, record *model.FileRecord, relativePath string, kek []byte) trashFileInfo {
	name := record.Name
	if relativePath == "/" {
		name = entry.OriginalName
	}
	originalPath, _ := trashOriginalPath(entry, relativePath, record.IsDir)
	info := store.FileInfo{
		Inode: record.ID, Generation: record.Generation, Name: name, IsDir: record.IsDir, Size: record.Size,
		CreatedAt: record.CreatedAt, LastModified: record.UpdatedAt,
		ContentType: record.ContentType, MediaWidth: record.MediaWidth,
		MediaHeight: record.MediaHeight, MediaDuration: record.MediaDuration,
		Status: record.Status,
	}
	h.fillThumbnail(&info, record, kek)
	return trashFileInfo{
		FileInfo: info, TrashID: entry.ID, RelativePath: relativePath,
		OriginalPath: originalPath, DeletedAt: entry.DeletedAt,
	}
}

func (h *Handler) notifyTrashChanged(userID string) {
	if h.Hub != nil {
		h.Hub.PushTrashChanged(userID)
	}
}

func (h *Handler) notifyVisibleParent(userID, username, storagePath string) {
	if h.Hub == nil {
		return
	}
	parent := parentDirOf(storagePath)
	if parent == "" || middleware.IsInternalStoragePath(parent) {
		return
	}
	h.Hub.PushDirChanged(userID, parent, middleware.ToAppPath(parent, username), "refresh")
}

func (h *Handler) moveRecord(userID string, record *model.FileRecord, destination string) error {
	if record.IsDir {
		return h.Repos.Files.MoveByPrefixNoReplace(userID, record.Path, destination)
	}
	name := filepath.Base(strings.TrimSuffix(destination, "/"))
	return h.Repos.Files.MoveNoReplace(userID, record.Path, destination, name)
}

func (h *Handler) moveVisibleRecordToTrash(userID string, record *model.FileRecord, destination string) error {
	records := []model.FileRecord{*record}
	if record.IsDir {
		var err error
		records, err = h.Repos.Files.ListByPrefix(userID, record.Path)
		if err != nil {
			return err
		}
	}
	if err := h.moveRecord(userID, record, destination); err != nil {
		return err
	}
	for index := range records {
		if !records[index].IsDir {
			h.revokeFileShares(userID, records[index].ID)
		}
	}
	return nil
}

func (h *Handler) handleCreateTrashEntry(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Path          string `json:"path"`
		ExpectedInode int64  `json:"expected_inode"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || body.Path == "" || body.ExpectedInode <= 0 {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
	}
	if normalizeAppPath(body.Path) == "/" {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	resolvedPath, err := middleware.ResolvePath(r, body.Path)
	if err != nil {
		return err
	}
	if resolvedPath == "/" {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	session := middleware.Session(r)
	record, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
	if err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "trash_failed"})
	}
	if record.ID != body.ExpectedInode {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "stale_file"})
	}
	if record.Status != "ready" {
		if record.OSSUploadID != "" {
			_ = h.Store.AbortMultipartUpload(record.StorageKey(), record.OSSUploadID)
		}
		if h.FileSystem != nil {
			_ = h.FileSystem.AbortDirectUpload(r.Context(), session.UserID, record.UploadID)
		} else {
			_ = h.Repos.Files.Delete(session.UserID, resolvedPath)
		}
		h.audit(r, "file_delete", body.Path, "upload_aborted", "success", 0)
		return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "trashed": false})
	}
	if err := h.ensureTrashStorage(session.UserID); err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "trash_failed"})
	}
	entry := &model.TrashEntry{
		ID: uuid.NewString(), UserID: session.UserID, RootInode: record.ID,
		OriginalPath: middleware.ToAppPath(record.Path, session.Username), OriginalName: record.Name, IsDir: record.IsDir,
		Size: record.Size, State: model.TrashStatePending, DeletedAt: time.Now().UTC(),
	}
	if err := h.Repos.Trash.Create(entry); err != nil {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "trash_conflict"})
	}
	destination := trashEntryStoragePath(entry)
	if err := h.moveVisibleRecordToTrash(session.UserID, record, destination); err != nil {
		_ = h.Repos.Trash.Delete(session.UserID, entry.ID)
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "trash_failed"})
	}
	status := http.StatusCreated
	state := model.TrashStateReady
	if err := h.Repos.Trash.MarkReady(session.UserID, entry.ID, model.TrashStatePending); err != nil {
		// The durable pending row and UUID payload path contain enough
		// information for startup recovery. Do not report the completed move as
		// a failed delete and tempt the client to repeat it.
		status = http.StatusAccepted
		state = model.TrashStatePending
	}
	h.notifyVisibleParent(session.UserID, session.Username, resolvedPath)
	h.notifyTrashChanged(session.UserID)
	h.audit(r, "file_trash", body.Path, entry.ID, "success", 0)
	return httpx.JSON(w, status, map[string]any{"ok": true, "id": entry.ID, "state": state})
}

func (h *Handler) handleListTrash(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	entries, err := h.Repos.Trash.List(session.UserID, httpx.QueryInt(r, "limit", 100), httpx.QueryInt(r, "offset", 0))
	if err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "trash_list_failed"})
	}
	kek, _ := h.getFileEncryptionKey(session)
	items := make([]trashFileInfo, 0, len(entries))
	for index := range entries {
		record, relativePath, recordErr := h.trashRecord(session.UserID, &entries[index], "/")
		if recordErr != nil {
			if errors.Is(recordErr, gorm.ErrRecordNotFound) || errors.Is(recordErr, errTrashRootInodeMismatch) {
				_ = h.Repos.Trash.Delete(session.UserID, entries[index].ID)
			}
			continue
		}
		items = append(items, h.trashFileProjection(&entries[index], record, relativePath, kek))
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"files": items})
}

func (h *Handler) readyTrashEntry(w http.ResponseWriter, r *http.Request) (*model.TrashEntry, *model.Session, error) {
	session := middleware.Session(r)
	entry, err := h.Repos.Trash.Get(session.UserID, r.PathValue("id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, session, httpx.NewError(http.StatusNotFound, "trash_not_found")
	}
	if err != nil {
		return nil, session, httpx.NewError(http.StatusInternalServerError, "trash_failed")
	}
	if entry.State != model.TrashStateReady {
		return nil, session, httpx.NewError(http.StatusConflict, "trash_busy")
	}
	return entry, session, nil
}

func sendTrashHandlerError(w http.ResponseWriter, r *http.Request, err error) error {
	if responseError, ok := err.(*httpx.Error); ok {
		return httpx.JSON(w, responseError.Code, map[string]any{"error": responseError.Message})
	}
	return err
}

func (h *Handler) handleListTrashDirectory(w http.ResponseWriter, r *http.Request) error {
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	record, _, err := h.trashRecord(session.UserID, entry, httpx.Query(r, "path", "/"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
	if err != nil || !record.IsDir {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "not_directory"})
	}
	records, err := h.Repos.Files.ListDirectChildren(session.UserID, record.Path)
	if err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "trash_list_failed"})
	}
	kek, _ := h.getFileEncryptionKey(session)
	items := make([]trashFileInfo, 0, len(records))
	for index := range records {
		relativePath, relativeErr := trashRelativePath(entry, records[index].Path)
		if relativeErr != nil {
			continue
		}
		items = append(items, h.trashFileProjection(entry, &records[index], relativePath, kek))
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{"files": items, "trash": entry})
}

func (h *Handler) handleTrashAccess(w http.ResponseWriter, r *http.Request) error {
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	record, relativePath, err := h.trashRecord(session.UserID, entry, httpx.Query(r, "path", "/"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
	if err != nil || record.IsDir {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	name := record.Name
	if relativePath == "/" {
		name = entry.OriginalName
	}
	return h.sendFileAccessResponse(w, r, session, record, name, "trash:"+entry.ID+relativePath)
}

func (h *Handler) handleTrashHistory(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	record, relativePath, err := h.trashRecord(session.UserID, entry, httpx.Query(r, "path", "/"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if err != nil || record.IsDir {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}
	_, versions, err := h.FileSystem.ListHistory(r.Context(), session.UserID, record.Path)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, dofs.ErrNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if errors.Is(err, dofs.ErrTypeMismatch) {
		return httpx.JSON(w, 400, map[string]any{"error": "not_file"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_failed"})
	}
	name := record.Name
	if relativePath == "/" {
		name = entry.OriginalName
	}
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"file": map[string]any{
			"inode":      record.ID,
			"name":       name,
			"path":       relativePath,
			"size":       record.Size,
			"generation": record.Generation,
		},
		"versions": versions,
	})
}

func (h *Handler) handleTrashHistoryAccess(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	generation, err := historyGeneration(r)
	if err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": err.Error()})
	}
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	record, relativePath, err := h.trashRecord(session.UserID, entry, httpx.Query(r, "path", "/"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if err != nil || record.IsDir {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}
	historical, err := h.FileSystem.GenerationAccess(r.Context(), session.UserID, record.Path, generation)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, dofs.ErrNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_access_failed"})
	}
	name := record.Name
	if relativePath == "/" {
		name = entry.OriginalName
	}
	return h.sendFileAccessResponse(w, r, session, &historical, name, "trash:"+entry.ID+relativePath+"#"+strconv.FormatInt(generation, 10))
}

func (h *Handler) handleTrashHistoryRestore(w http.ResponseWriter, r *http.Request) error {
	if h.FileSystem == nil {
		return httpx.JSON(w, 503, map[string]any{"error": "dofs_unavailable"})
	}
	var body struct {
		Path               string `json:"path"`
		Generation         int64  `json:"generation"`
		ExpectedGeneration int64  `json:"expected_generation"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil || body.Path == "" || body.Generation <= 0 || body.ExpectedGeneration <= 0 {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_request"})
	}
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	record, _, err := h.trashRecord(session.UserID, entry, body.Path)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if err != nil || record.IsDir {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_path"})
	}
	restored, err := h.FileSystem.RestoreGeneration(r.Context(), session.UserID, record.Path, body.Generation, body.ExpectedGeneration)
	if errors.Is(err, dofs.ErrConflict) {
		return httpx.JSON(w, 409, map[string]any{"error": "generation_conflict"})
	}
	if errors.Is(err, dofs.ErrQuotaExceeded) {
		return httpx.JSON(w, 507, map[string]any{"error": "quota_exceeded"})
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, dofs.ErrNotFound) {
		return httpx.JSON(w, 404, map[string]any{"error": "not_found"})
	}
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "history_restore_failed"})
	}
	h.audit(r, "file_history_restore", "trash:"+entry.ID+body.Path, strconv.FormatInt(body.Generation, 10), "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "generation": restored.Generation, "size": restored.Size})
}

func (h *Handler) nextAvailableRestorePath(userID, targetPath string, isDir bool) (string, error) {
	parent := parentDirOf(targetPath)
	records, err := h.Repos.Files.ListDirectChildren(userID, parent)
	if err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(records))
	for index := range records {
		used[records[index].Name] = struct{}{}
	}
	name := filepath.Base(strings.TrimSuffix(targetPath, "/"))
	extension := ""
	base := name
	if !isDir {
		extension = filepath.Ext(name)
		if extension != "" && extension != name {
			base = strings.TrimSuffix(name, extension)
		}
	}
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, extension)
		if _, exists := used[candidate]; exists {
			continue
		}
		result := parent + candidate
		if isDir {
			result += "/"
		}
		return result, nil
	}
}

type trashConflict struct {
	RelativePath string `json:"relative_path"`
	TargetPath   string `json:"target_path"`
	Name         string `json:"name"`
	IsDir        bool   `json:"is_dir"`
	Size         int64  `json:"size"`
	ExistingName string `json:"existing_name"`
	ExistingDir  bool   `json:"existing_is_dir"`
	ExistingSize int64  `json:"existing_size"`
}

func (h *Handler) collectMergeConflicts(userID string, entry *model.TrashEntry, source, target *model.FileRecord, conflicts *[]trashConflict) error {
	children, err := h.Repos.Files.ListDirectChildren(userID, source.Path)
	if err != nil {
		return err
	}
	for index := range children {
		child := &children[index]
		targetPath := target.Path + child.Name
		if child.IsDir {
			targetPath += "/"
		}
		existing, lookupErr := h.Repos.Files.Get(userID, targetPath)
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			continue
		}
		if lookupErr != nil {
			return lookupErr
		}
		if child.IsDir && existing.IsDir {
			if err := h.collectMergeConflicts(userID, entry, child, existing, conflicts); err != nil {
				return err
			}
			continue
		}
		relativePath, _ := trashRelativePath(entry, child.Path)
		*conflicts = append(*conflicts, trashConflict{
			RelativePath: relativePath, TargetPath: targetPath, Name: child.Name,
			IsDir: child.IsDir, Size: child.Size, ExistingName: existing.Name,
			ExistingDir: existing.IsDir, ExistingSize: existing.Size,
		})
	}
	return nil
}

func (h *Handler) mergeTrashDirectory(userID string, source, target *model.FileRecord, conflict string) error {
	children, err := h.Repos.Files.ListDirectChildren(userID, source.Path)
	if err != nil {
		return err
	}
	for index := range children {
		child := &children[index]
		targetPath := target.Path + child.Name
		if child.IsDir {
			targetPath += "/"
		}
		existing, lookupErr := h.Repos.Files.Get(userID, targetPath)
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			if err := h.moveRecord(userID, child, targetPath); err != nil {
				return err
			}
			continue
		}
		if lookupErr != nil {
			return lookupErr
		}
		if child.IsDir && existing.IsDir {
			if err := h.mergeTrashDirectory(userID, child, existing, conflict); err != nil {
				return err
			}
			remaining, listErr := h.Repos.Files.ListDirectChildren(userID, child.Path)
			if listErr != nil {
				return listErr
			}
			if len(remaining) == 0 {
				if err := h.Repos.Files.Delete(userID, child.Path); err != nil {
					return err
				}
			}
			continue
		}
		switch conflict {
		case "skip":
			continue
		case "replace":
			if err := h.permanentlyDeletePath(userID, existing.Path, existing.IsDir); err != nil {
				return err
			}
		case "rename":
			targetPath, err = h.nextAvailableRestorePath(userID, targetPath, child.IsDir)
			if err != nil {
				return err
			}
		default:
			return errors.New("unresolved merge conflict")
		}
		if err := h.moveRecord(userID, child, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) finishTrashMutation(userID string, entry *model.TrashEntry, fromState string) error {
	root, err := h.Repos.Files.GetByID(userID, entry.RootInode)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return h.Repos.Trash.Delete(userID, entry.ID)
	}
	if err != nil {
		return err
	}
	rootStoragePath := trashEntryStoragePath(entry)
	if root.Path != rootStoragePath {
		return h.Repos.Trash.Delete(userID, entry.ID)
	}
	if root.IsDir {
		children, listErr := h.Repos.Files.ListDirectChildren(userID, root.Path)
		if listErr != nil {
			return listErr
		}
		if len(children) == 0 {
			if deleteErr := h.permanentlyDeletePath(userID, root.Path, true); deleteErr != nil {
				return deleteErr
			}
			return h.Repos.Trash.Delete(userID, entry.ID)
		}
	}
	return h.Repos.Trash.MarkReady(userID, entry.ID, fromState)
}

func (h *Handler) restoreConflictResponse(w http.ResponseWriter, r *http.Request, targetPath string, source, existing *model.FileRecord, merge bool, conflicts []trashConflict) error {
	return httpx.JSON(w, http.StatusConflict, map[string]any{
		"error": "restore_conflict", "target_path": targetPath, "merge_available": merge,
		"incoming":  map[string]any{"name": path.Base(targetPath), "size": source.Size, "is_dir": source.IsDir},
		"existing":  map[string]any{"name": existing.Name, "size": existing.Size, "is_dir": existing.IsDir},
		"conflicts": conflicts,
	})
}

func (h *Handler) handleRestoreTrash(w http.ResponseWriter, r *http.Request) error {
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	var body trashRestoreRequest
	if err := httpx.DecodeJSON(r, &body); err != nil || body.ExpectedInode <= 0 || !validateTrashConflict(body.Conflict, true) || !validateTrashConflict(body.NestedConflict, false) {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
	}
	record, relativePath, err := h.trashRecord(session.UserID, entry, body.Path)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
	if err != nil {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	if body.ExpectedInode != record.ID {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "stale_file"})
	}
	targetPath, err := trashOriginalPath(entry, relativePath, record.IsDir)
	if err != nil {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	existing, lookupErr := h.Repos.Files.Get(session.UserID, targetPath)
	hasExisting := lookupErr == nil
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_failed"})
	}
	if hasExisting && body.Conflict == "" {
		return h.restoreConflictResponse(w, r, targetPath, record, existing, record.IsDir && existing.IsDir, nil)
	}
	if hasExisting && body.Conflict == "skip" {
		return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true})
	}
	if hasExisting && body.Conflict == "rename" {
		targetPath, err = h.nextAvailableRestorePath(session.UserID, targetPath, record.IsDir)
		if err != nil {
			return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_failed"})
		}
		hasExisting = false
	}
	if hasExisting && body.Conflict == "merge" {
		if !record.IsDir || !existing.IsDir {
			return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "merge_not_available"})
		}
		conflicts := make([]trashConflict, 0)
		if err := h.collectMergeConflicts(session.UserID, entry, record, existing, &conflicts); err != nil {
			return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_failed"})
		}
		if len(conflicts) > 0 && body.NestedConflict == "" {
			return h.restoreConflictResponse(w, r, targetPath, record, existing, true, conflicts)
		}
		if err := h.Repos.Trash.Transition(session.UserID, entry.ID, model.TrashStateReady, model.TrashStateRestoring, record.ID, relativePath, targetPath); err != nil {
			return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "trash_busy"})
		}
		entry.State = model.TrashStateRestoring
		entry.OperationInode = record.ID
		entry.OperationPath = relativePath
		entry.TargetPath = targetPath
		if err := h.mergeTrashDirectory(session.UserID, record, existing, body.NestedConflict); err != nil {
			return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_failed"})
		}
		remaining, listErr := h.Repos.Files.ListDirectChildren(session.UserID, record.Path)
		if listErr == nil && len(remaining) == 0 {
			_ = h.Repos.Files.Delete(session.UserID, record.Path)
		}
		if err := h.finishTrashMutation(session.UserID, entry, model.TrashStateRestoring); err != nil {
			return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_pending"})
		}
		h.notifyVisibleParent(session.UserID, session.Username, targetPath)
		h.notifyTrashChanged(session.UserID)
		h.audit(r, "trash_restore", entry.ID, targetPath, "success", 0)
		return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "path": targetPath})
	}
	if hasExisting && body.Conflict != "replace" {
		return h.restoreConflictResponse(w, r, targetPath, record, existing, record.IsDir && existing.IsDir, nil)
	}
	if err := h.Repos.Trash.Transition(session.UserID, entry.ID, model.TrashStateReady, model.TrashStateRestoring, record.ID, relativePath, targetPath); err != nil {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "trash_busy"})
	}
	entry.State = model.TrashStateRestoring
	entry.OperationInode = record.ID
	entry.OperationPath = relativePath
	entry.TargetPath = targetPath
	if err := h.ensureParentDirRecords(session.UserID, targetPath, record.IsDir); err != nil {
		_ = h.Repos.Trash.MarkReady(session.UserID, entry.ID, model.TrashStateRestoring)
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "restore_parent_conflict"})
	}
	if hasExisting {
		if err := h.permanentlyDeletePath(session.UserID, existing.Path, existing.IsDir); err != nil {
			return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_failed"})
		}
	}
	if err := h.moveRecord(session.UserID, record, targetPath); err != nil {
		_ = h.Repos.Trash.MarkReady(session.UserID, entry.ID, model.TrashStateRestoring)
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_failed"})
	}
	if err := h.finishTrashMutation(session.UserID, entry, model.TrashStateRestoring); err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "restore_pending"})
	}
	h.notifyVisibleParent(session.UserID, session.Username, targetPath)
	h.notifyTrashChanged(session.UserID)
	h.audit(r, "trash_restore", entry.ID, targetPath, "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "path": targetPath})
}

func (h *Handler) handleDeleteTrashItem(w http.ResponseWriter, r *http.Request) error {
	entry, session, err := h.readyTrashEntry(w, r)
	if err != nil {
		return sendTrashHandlerError(w, r, err)
	}
	record, relativePath, err := h.trashRecord(session.UserID, entry, httpx.Query(r, "path", "/"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
	if err != nil {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_path"})
	}
	expectedInode, parseErr := strconv.ParseInt(httpx.Query(r, "expected_inode"), 10, 64)
	if parseErr != nil || expectedInode <= 0 {
		return httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "expected_inode_required"})
	}
	if expectedInode != record.ID {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "stale_file"})
	}
	if err := h.Repos.Trash.Transition(session.UserID, entry.ID, model.TrashStateReady, model.TrashStateDeleting, record.ID, relativePath, ""); err != nil {
		return httpx.JSON(w, http.StatusConflict, map[string]any{"error": "trash_busy"})
	}
	entry.State = model.TrashStateDeleting
	entry.OperationInode = record.ID
	entry.OperationPath = relativePath
	if err := h.permanentlyDeletePath(session.UserID, record.Path, record.IsDir); err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "delete_failed"})
	}
	if err := h.finishTrashMutation(session.UserID, entry, model.TrashStateDeleting); err != nil {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "delete_pending"})
	}
	_, remainingErr := h.Repos.Trash.Get(session.UserID, entry.ID)
	entryRemoved := errors.Is(remainingErr, gorm.ErrRecordNotFound)
	h.notifyTrashChanged(session.UserID)
	h.audit(r, "trash_delete", entry.ID, relativePath, "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "entry_removed": entryRemoved})
}

func (h *Handler) handleEmptyTrash(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	failed := make([]string, 0)
	failedSet := make(map[string]struct{})
	appendFailed := func(id string) {
		if _, exists := failedSet[id]; exists {
			return
		}
		failedSet[id] = struct{}{}
		failed = append(failed, id)
	}
	offset := 0
	for {
		entries, err := h.Repos.Trash.List(session.UserID, 500, offset)
		if err != nil {
			return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "trash_list_failed"})
		}
		if len(entries) == 0 {
			break
		}
		retained := 0
		for index := range entries {
			entry := &entries[index]
			record, _, recordErr := h.trashRecord(session.UserID, entry, "/")
			if errors.Is(recordErr, gorm.ErrRecordNotFound) {
				if deleteErr := h.Repos.Trash.Delete(session.UserID, entry.ID); deleteErr != nil {
					appendFailed(entry.ID)
					retained++
				}
				continue
			}
			if recordErr != nil {
				appendFailed(entry.ID)
				retained++
				continue
			}
			if transitionErr := h.Repos.Trash.Transition(session.UserID, entry.ID, model.TrashStateReady, model.TrashStateDeleting, entry.RootInode, "/", ""); transitionErr != nil {
				appendFailed(entry.ID)
				current, getErr := h.Repos.Trash.Get(session.UserID, entry.ID)
				if getErr == nil && current.State == model.TrashStateReady {
					retained++
				}
				continue
			}
			entry.OperationPath = "/"
			if deleteErr := h.permanentlyDeletePath(session.UserID, record.Path, record.IsDir); deleteErr != nil {
				appendFailed(entry.ID)
				continue
			}
			if deleteErr := h.Repos.Trash.Delete(session.UserID, entry.ID); deleteErr != nil {
				appendFailed(entry.ID)
			}
		}
		offset += retained
		if len(entries) < 500 {
			break
		}
	}
	h.notifyTrashChanged(session.UserID)
	if len(failed) > 0 {
		return httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": "empty_trash_partial", "failed": failed})
	}
	h.audit(r, "trash_empty", "", "", "success", 0)
	return httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) listAllReadyTrashEntries(userID string) ([]model.TrashEntry, error) {
	entries := make([]model.TrashEntry, 0)
	for offset := 0; ; offset += 500 {
		page, err := h.Repos.Trash.List(userID, 500, offset)
		if err != nil {
			return nil, err
		}
		entries = append(entries, page...)
		if len(page) < 500 {
			return entries, nil
		}
	}
}

// PrepareTrashStorage performs the one intentionally destructive legacy
// migration authorised for this iteration. Only roots backed by a v2 database
// entry survive below /.domus/trash/items; all path-shaped v1 payloads and
// orphan v2 payloads are permanently removed.
func (h *Handler) PrepareTrashStorage() error {
	users, err := h.Repos.Users.List()
	if err != nil {
		return err
	}
	recoverable, err := h.Repos.Trash.ListRecoverable()
	if err != nil {
		return err
	}
	recoverableByUser := make(map[string][]model.TrashEntry)
	for index := range recoverable {
		entry := recoverable[index]
		recoverableByUser[entry.UserID] = append(recoverableByUser[entry.UserID], entry)
	}
	for index := range users {
		userID := users[index].ID
		ready, err := h.listAllReadyTrashEntries(userID)
		if err != nil {
			return err
		}
		entries := append(ready, recoverableByUser[userID]...)
		expectedRoots := make(map[string]int64, len(entries))
		for entryIndex := range entries {
			entry := &entries[entryIndex]
			root, rootErr := h.Repos.Files.GetByID(userID, entry.RootInode)
			if rootErr == nil && root.Path == trashEntryStoragePath(entry) {
				expectedRoots[root.Path] = root.ID
				continue
			}
			if rootErr != nil && !errors.Is(rootErr, gorm.ErrRecordNotFound) {
				return rootErr
			}
			if entry.State == model.TrashStateReady {
				if deleteErr := h.Repos.Trash.Delete(userID, entry.ID); deleteErr != nil {
					return deleteErr
				}
			}
		}

		children, err := h.Repos.Files.ListDirectChildren(userID, trashStorageRootPath)
		if err != nil {
			return err
		}
		itemsDirectoryExists := false
		for childIndex := range children {
			child := &children[childIndex]
			if child.Path == trashItemsStorageRootPath && child.IsDir {
				itemsDirectoryExists = true
				continue
			}
			if err := h.permanentlyDeletePath(userID, child.Path, child.IsDir); err != nil {
				return fmt.Errorf("delete legacy trash payload %s: %w", child.Path, err)
			}
		}
		if !itemsDirectoryExists {
			if err := h.ensureTrashStorage(userID); err != nil {
				return err
			}
		}
		itemRoots, err := h.Repos.Files.ListDirectChildren(userID, trashItemsStorageRootPath)
		if err != nil {
			return err
		}
		for childIndex := range itemRoots {
			child := &itemRoots[childIndex]
			expectedInode, exists := expectedRoots[child.Path]
			if exists && expectedInode == child.ID {
				continue
			}
			if err := h.permanentlyDeletePath(userID, child.Path, child.IsDir); err != nil {
				return fmt.Errorf("delete orphan trash payload %s: %w", child.Path, err)
			}
		}
	}
	return h.RecoverTrash(true)
}

func (h *Handler) recoverTrashEntry(entry *model.TrashEntry) error {
	root, rootErr := h.Repos.Files.GetByID(entry.UserID, entry.RootInode)
	switch entry.State {
	case model.TrashStatePending:
		if errors.Is(rootErr, gorm.ErrRecordNotFound) {
			return h.Repos.Trash.Delete(entry.UserID, entry.ID)
		}
		if rootErr != nil {
			return rootErr
		}
		if root.Path == trashEntryStoragePath(entry) {
			return h.Repos.Trash.MarkReady(entry.UserID, entry.ID, model.TrashStatePending)
		}
		// The rename never happened or the inode has already been restored.
		return h.Repos.Trash.Delete(entry.UserID, entry.ID)
	case model.TrashStateRestoring:
		if errors.Is(rootErr, gorm.ErrRecordNotFound) {
			return h.Repos.Trash.Delete(entry.UserID, entry.ID)
		}
		if rootErr != nil {
			return rootErr
		}
		return h.finishTrashMutation(entry.UserID, entry, model.TrashStateRestoring)
	case model.TrashStateDeleting:
		operation, operationErr := h.Repos.Files.GetByID(entry.UserID, entry.OperationInode)
		if operationErr == nil && strings.HasPrefix(operation.Path, trashEntryStoragePath(entry)) {
			if err := h.permanentlyDeletePath(entry.UserID, operation.Path, operation.IsDir); err != nil {
				return err
			}
		} else if operationErr != nil && !errors.Is(operationErr, gorm.ErrRecordNotFound) {
			return operationErr
		}
		return h.finishTrashMutation(entry.UserID, entry, model.TrashStateDeleting)
	default:
		return errors.New("unknown trash state")
	}
}

// RecoverTrash reconciles durable cross-database operations. force is used at
// startup when no live request can own an entry; periodic passes observe a
// grace period so they never race a slow recursive mutation.
func (h *Handler) RecoverTrash(force bool) error {
	entries, err := h.Repos.Trash.ListRecoverable()
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-trashRecoveryGrace)
	recoveryErrors := make([]error, 0)
	for index := range entries {
		if !force && entries[index].UpdatedAt.After(cutoff) {
			continue
		}
		if err := h.recoverTrashEntry(&entries[index]); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("recover trash entry %s: %w", entries[index].ID, err))
			continue
		}
		h.notifyTrashChanged(entries[index].UserID)
	}
	return errors.Join(recoveryErrors...)
}

func (h *Handler) StartTrashRecovery(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := h.RecoverTrash(false); err != nil {
					log.Printf("[trash-recovery] %v", err)
				}
			}
		}
	}()
}
