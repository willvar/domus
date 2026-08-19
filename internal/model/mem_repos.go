package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// NewMemRepos creates a fully in-memory Repos suitable for testing.
// ---------------------------------------------------------------------------

func NewMemRepos(onTaskUpdate TaskUpdateFunc) *Repos {
	users := &memUserRepo{data: make(map[string]*User)}
	files := &memFileRepo{data: make(map[string]*FileRecord), nextID: 1}
	sessions := &memSessionRepo{data: make(map[string]*memSessionEntry)}
	tasks := &memTaskRepo{data: make(map[string]*Task), nextID: 1, onTaskUpdate: onTaskUpdate}
	shares := &memShareRepo{data: make(map[string]*Share), nextID: 1, users: users}
	workspace := &memWorkspaceRepo{data: make(map[string]string)}
	audit := &memAuditRepo{}
	cleanup := &memUserCleanupRepo{
		users:     users,
		files:     files,
		sessions:  sessions,
		tasks:     tasks,
		shares:    shares,
		workspace: workspace,
	}

	return &Repos{
		Users:     users,
		Files:     files,
		Sessions:  sessions,
		Tasks:     tasks,
		Audit:     audit,
		Shares:    shares,
		Workspace: workspace,
		Cleanup:   cleanup,
	}
}

// ---------------------------------------------------------------------------
// memUserRepo
// ---------------------------------------------------------------------------

type memUserRepo struct {
	mu   sync.Mutex
	data map[string]*User
}

func (r *memUserRepo) Create(username, password, role, wrappedKEK string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, u := range r.data {
		if u.Username == username {
			return nil, fmt.Errorf("username %q already exists", username)
		}
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		WrappedKEK:   wrappedKEK,
		CreatedAt:    time.Now(),
	}
	r.data[user.ID] = user
	return copyUser(user), nil
}

func (r *memUserRepo) GetByID(id string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return copyUser(u), nil
}

func (r *memUserRepo) GetByUsername(username string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.data {
		if u.Username == username {
			return copyUser(u), nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *memUserRepo) List() ([]User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]User, 0, len(r.data))
	for _, u := range r.data {
		out = append(out, *copyUser(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *memUserRepo) UpdateRole(id, role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	u.Role = role
	return nil
}

func (r *memUserRepo) UpdatePassword(id, password string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return nil
}

func (r *memUserRepo) UpdateEmail(id, email string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	u.Email = email
	return nil
}

func (r *memUserRepo) UpdateTOTP(id, secret string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	u.TOTPSecret = secret
	u.TOTPEnabled = enabled
	return nil
}

func (r *memUserRepo) UpdateDisplayName(id, displayName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	u.DisplayName = displayName
	return nil
}

func (r *memUserRepo) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *memUserRepo) Count() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.data), nil
}

func (r *memUserRepo) CountByRole(role string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, u := range r.data {
		if u.Role == role {
			n++
		}
	}
	return n, nil
}

func (r *memUserRepo) GetWrappedKEK(userID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[userID]
	if !ok {
		return "", gorm.ErrRecordNotFound
	}
	return u.WrappedKEK, nil
}

func (r *memUserRepo) SetWrappedKEK(userID, wrappedKEK string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.data[userID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	u.WrappedKEK = wrappedKEK
	return nil
}

func copyUser(u *User) *User {
	c := *u
	return &c
}

// ---------------------------------------------------------------------------
// memFileRepo
// ---------------------------------------------------------------------------

type memFileRepo struct {
	mu     sync.Mutex
	data   map[string]*FileRecord // key = userID + ":" + path
	nextID int64
}

func fileKey(userID, path string) string { return userID + ":" + path }

func (r *memFileRepo) Upsert(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fileKey(userID, path)
	now := time.Now()
	existing, ok := r.data[key]
	if !ok {
		rec := &FileRecord{
			ID:          r.nextID,
			UserID:      userID,
			Path:        path,
			Parent:      memParentOf(path),
			Name:        name,
			IsDir:       isDir,
			Size:        size,
			ContentType: contentType,
			ContentHash: contentHash,
			Status:      "ready",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if len(opts) > 0 {
			rec.WrappedDEK = opts[0].WrappedDEK
			rec.ObjectKey = opts[0].ObjectKey
		}
		if !isDir {
			rec.Generation = 1
		}
		r.nextID++
		r.data[key] = rec
		return nil
	}
	existing.Name = name
	existing.Parent = memParentOf(path)
	existing.IsDir = isDir
	existing.Size = size
	existing.ContentType = contentType
	existing.Status = "ready"
	existing.UpdatedAt = now
	if contentHash != "" {
		existing.ContentHash = contentHash
	}
	if len(opts) > 0 && opts[0].WrappedDEK != "" {
		existing.WrappedDEK = opts[0].WrappedDEK
	}
	if !isDir {
		if len(opts) > 0 && opts[0].ObjectKey != "" {
			existing.ObjectKey = opts[0].ObjectKey
		}
		existing.Generation++
	}
	return nil
}

func (r *memFileRepo) Get(userID, path string) (*FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.data[fileKey(userID, path)]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	c := *rec
	return &c, nil
}

func (r *memFileRepo) GetByID(userID string, id int64) (*FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.UserID == userID && rec.ID == id {
			copy := *rec
			return &copy, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *memFileRepo) getByStorageKeyLocked(userID, objectKey string) *FileRecord {
	var found *FileRecord
	for _, rec := range r.data {
		if rec.UserID == userID && rec.StorageKey() == objectKey && (found == nil || rec.ID > found.ID) {
			found = rec
		}
	}
	return found
}

func (r *memFileRepo) GetByStorageKey(userID, objectKey string) (*FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.getByStorageKeyLocked(userID, objectKey)
	if rec == nil {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *rec
	return &copy, nil
}

func (r *memFileRepo) retireLinkedThumbnailLocked(source *FileRecord) {
	if source == nil || source.ThumbnailKey == "" {
		return
	}
	var thumbnail *FileRecord
	for _, candidate := range r.data {
		if candidate.UserID == source.UserID && candidate.ID != source.ID && candidate.Status == "ready" &&
			candidate.StorageKey() == source.ThumbnailKey && (thumbnail == nil || candidate.ID > thumbnail.ID) {
			thumbnail = candidate
		}
	}
	if thumbnail != nil {
		delete(r.data, fileKey(thumbnail.UserID, thumbnail.Path))
	}
}

func (r *memFileRepo) Delete(userID, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, fileKey(userID, path))
	return nil
}

func (r *memFileRepo) DeleteByPrefix(userID, prefix string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, rec := range r.data {
		if rec.UserID == userID && strings.HasPrefix(rec.Path, prefix) {
			delete(r.data, k)
		}
	}
	return nil
}

func (r *memFileRepo) ListByPrefix(userID, prefix string) ([]FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []FileRecord
	for _, rec := range r.data {
		if rec.UserID == userID && strings.HasPrefix(rec.Path, prefix) {
			c := *rec
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *memFileRepo) ListDirectChildren(userID, parent string) ([]FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []FileRecord
	for _, rec := range r.data {
		if rec.UserID == userID && rec.Parent == parent && rec.Status != "deleted" {
			c := *rec
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (r *memFileRepo) ListAllChildren(userID, parent string) ([]FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []FileRecord
	for _, rec := range r.data {
		if rec.UserID == userID && rec.Parent == parent {
			c := *rec
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *memFileRepo) Move(userID, oldPath, newPath, newName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	oldKey := fileKey(userID, oldPath)
	rec, ok := r.data[oldKey]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	delete(r.data, oldKey)
	rec.Path = newPath
	rec.Parent = memParentOf(newPath)
	rec.Name = newName
	rec.UpdatedAt = time.Now()
	r.data[fileKey(userID, newPath)] = rec
	return nil
}

func (r *memFileRepo) MoveByPrefix(userID, oldPrefix, newPrefix string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var toMove []*FileRecord
	for k, rec := range r.data {
		if rec.UserID == userID && strings.HasPrefix(rec.Path, oldPrefix) {
			delete(r.data, k)
			toMove = append(toMove, rec)
		}
	}
	now := time.Now()
	for _, rec := range toMove {
		rec.Path = newPrefix + rec.Path[len(oldPrefix):]
		rec.Parent = strings.Replace(rec.Parent, oldPrefix, newPrefix, 1)
		rec.UpdatedAt = now
		r.data[fileKey(userID, rec.Path)] = rec
	}
	return nil
}

func (r *memFileRepo) UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.data[fileKey(userID, path)]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	if rec.ThumbnailKey != thumbnailKey {
		r.retireLinkedThumbnailLocked(rec)
	}
	rec.ThumbnailKey = thumbnailKey
	rec.ThumbnailWrappedDEK = thumbnailWrappedDEK
	rec.MediaWidth = width
	rec.MediaHeight = height
	rec.MediaDuration = duration
	rec.UpdatedAt = time.Now()
	return nil
}

func (r *memFileRepo) UpdateThumbnailIfGeneration(userID, path string, fileID, generation int64, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.data[fileKey(userID, path)]
	if !ok || rec.ID != fileID || rec.Generation != generation || rec.Status != "ready" {
		return false, nil
	}
	if rec.ThumbnailKey != thumbnailKey {
		r.retireLinkedThumbnailLocked(rec)
	}
	rec.ThumbnailKey = thumbnailKey
	rec.ThumbnailWrappedDEK = thumbnailWrappedDEK
	rec.MediaWidth = width
	rec.MediaHeight = height
	rec.MediaDuration = duration
	rec.UpdatedAt = time.Now()
	return true, nil
}

func (r *memFileRepo) UpdateContentType(userID, path, contentType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.data[fileKey(userID, path)]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	rec.ContentType = contentType
	rec.UpdatedAt = time.Now()
	return nil
}

func (r *memFileRepo) SearchFiles(userID, query string, limit int) ([]SearchFileResult, error) {
	if limit <= 0 {
		limit = 50
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SearchFileResult
	lowerQ := strings.ToLower(query)
	for _, rec := range r.data {
		if rec.UserID == userID && rec.Status == "ready" &&
			!strings.HasPrefix(rec.Path, "/.domus/") &&
			strings.Contains(strings.ToLower(rec.Name), lowerQ) {
			c := *rec
			out = append(out, SearchFileResult{FileRecord: c, Rank: 1.0})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *memFileRepo) CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64, contentType, clientInstanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fileKey(userID, path)
	now := time.Now()
	rec := &FileRecord{
		ID:               r.nextID,
		UserID:           userID,
		Path:             path,
		ObjectKey:        ".dofs/test-objects/" + userID + "/" + uploadID,
		Parent:           memParentOf(path),
		Name:             name,
		Size:             fileSize,
		ContentType:      contentType,
		Status:           "uploading",
		UploadID:         uploadID,
		TaskID:           taskID,
		OSSUploadID:      ossUploadID,
		ClientInstanceID: clientInstanceID,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	r.nextID++
	r.data[key] = rec
	return nil
}

func (r *memFileRepo) GetUpload(userID, uploadID string) (*FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.UploadID == uploadID && rec.UserID == userID {
			c := *rec
			return &c, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *memFileRepo) UpdateStatus(uploadID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.UploadID == uploadID {
			rec.Status = status
			rec.UpdatedAt = time.Now()
			return nil
		}
	}
	return nil
}

func (r *memFileRepo) TouchUpload(uploadID string, seenAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.UploadID == uploadID {
			rec.LastSeenAt = seenAt
			rec.UpdatedAt = seenAt
			return nil
		}
	}
	return nil
}

func (r *memFileRepo) ListActiveUploads(userID string) ([]FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []FileRecord
	for _, rec := range r.data {
		if rec.UserID == userID && rec.Status == "uploading" {
			c := *rec
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *memFileRepo) CancelUploadsForOtherInstances(userID, clientInstanceID string, cutoff time.Time) ([]FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []FileRecord
	for _, rec := range r.data {
		if rec.UserID != userID || rec.Status != "uploading" {
			continue
		}
		if !rec.LastSeenAt.Before(cutoff) {
			continue
		}
		if clientInstanceID != "" && rec.ClientInstanceID == clientInstanceID {
			continue
		}
		rec.Status = "cancelled"
		rec.UpdatedAt = time.Now()
		c := *rec
		out = append(out, c)
	}
	return out, nil
}

func (r *memFileRepo) GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-staleAfter)
	var out []FileRecord
	for _, rec := range r.data {
		if rec.Status == "uploading" && rec.UpdatedAt.Before(cutoff) {
			c := *rec
			out = append(out, c)
		}
	}
	return out, nil
}

// memParentOf mirrors the unexported parentOf helper in file.go.
func memParentOf(path string) string {
	p := strings.TrimSuffix(path, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx+1]
	}
	return ""
}

// ---------------------------------------------------------------------------
// memSessionRepo
// ---------------------------------------------------------------------------

type memSessionEntry struct {
	Session   *DBSession
	ExpiresAt time.Time
}

type memSessionRepo struct {
	mu          sync.Mutex
	data        map[string]*memSessionEntry
	populateKEK func(*Session)
}

func (r *memSessionRepo) Create(userID, username, role string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	now := time.Now()
	r.data[id] = &memSessionEntry{
		Session: &DBSession{
			ID:        id,
			UserID:    userID,
			Username:  username,
			Role:      role,
			CreatedAt: now,
			ExpiresAt: now.Add(7 * 24 * time.Hour),
		},
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}
	return id, nil
}

func (r *memSessionRepo) Get(id string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.data[id]
	if !ok || entry.ExpiresAt.Before(time.Now()) {
		return nil
	}
	s := &Session{
		UserID:    entry.Session.UserID,
		Username:  entry.Session.Username,
		Role:      entry.Session.Role,
		CreatedAt: entry.Session.CreatedAt,
	}
	if r.populateKEK != nil {
		r.populateKEK(s)
	}
	return s
}

func (r *memSessionRepo) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
}

func (r *memSessionRepo) DeleteByUserID(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, entry := range r.data {
		if entry.Session.UserID == userID {
			delete(r.data, id)
		}
	}
}

func (r *memSessionRepo) DeleteByUserIDExcept(userID, exceptSessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, entry := range r.data {
		if entry.Session.UserID == userID && id != exceptSessionID {
			delete(r.data, id)
		}
	}
}

func (r *memSessionRepo) CleanExpired() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for id, entry := range r.data {
		if entry.ExpiresAt.Before(now) {
			delete(r.data, id)
		}
	}
}

func (r *memSessionRepo) SetPopulateKEK(fn func(*Session)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.populateKEK = fn
}

// ---------------------------------------------------------------------------
// memTaskRepo
// ---------------------------------------------------------------------------

type memTaskRepo struct {
	mu           sync.Mutex
	data         map[string]*Task
	nextID       int64
	onTaskUpdate TaskUpdateFunc
}

func (r *memTaskRepo) Create(userID, taskID, taskType, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.data[taskID] = &Task{
		ID:        r.nextID,
		UserID:    userID,
		TaskID:    taskID,
		Type:      taskType,
		Status:    "running",
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.nextID++
	return nil
}

func (r *memTaskRepo) Get(taskID string) (*Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.data[taskID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	c := *t
	return &c, nil
}

func (r *memTaskRepo) UpdateProgress(taskID string, progress float64, phase string) error {
	r.mu.Lock()
	t, ok := r.data[taskID]
	if !ok {
		r.mu.Unlock()
		return gorm.ErrRecordNotFound
	}
	t.Progress = progress
	t.Phase = phase
	t.UpdatedAt = time.Now()
	// Copy for callback
	tc := *t
	cb := r.onTaskUpdate
	r.mu.Unlock()

	if cb != nil {
		cb(tc.UserID, tc.TaskID, tc.Type, tc.Name, tc.Status, tc.ClientInstanceID, progress, phase)
	}
	return nil
}

func (r *memTaskRepo) UpdateStatus(taskID, status string) error {
	r.mu.Lock()
	t, ok := r.data[taskID]
	if !ok {
		r.mu.Unlock()
		return gorm.ErrRecordNotFound
	}
	t.Status = status
	t.UpdatedAt = time.Now()
	if status == "completed" {
		t.Progress = 1.0
	}
	tc := *t
	cb := r.onTaskUpdate
	r.mu.Unlock()

	if cb != nil {
		cb(tc.UserID, tc.TaskID, tc.Type, tc.Name, status, tc.ClientInstanceID, tc.Progress, tc.Phase)
	}
	return nil
}

func (r *memTaskRepo) ListRecent(userID string) ([]Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Task
	for _, t := range r.data {
		if t.UserID == userID {
			c := *t
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

func (r *memTaskRepo) DeleteCompleted(userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for taskID, t := range r.data {
		if t.UserID == userID && (t.Status == "completed" || t.Status == "failed" || t.Status == "cancelled") {
			delete(r.data, taskID)
		}
	}
	return nil
}

func (r *memTaskRepo) Delete(taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, taskID)
	return nil
}

// ---------------------------------------------------------------------------
// memShareRepo
// ---------------------------------------------------------------------------

type memShareRepo struct {
	mu     sync.Mutex
	data   map[string]*Share // keyed by ShareID
	nextID int64
	users  *memUserRepo
}

func (r *memShareRepo) Create(share *Share) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if share == nil || share.FileInode <= 0 {
		return ErrInvalidShareInode
	}
	if share.ID == 0 {
		share.ID = r.nextID
		r.nextID++
	}
	if share.CreatedAt.IsZero() {
		share.CreatedAt = time.Now()
	}
	stored := *share
	r.data[share.ShareID] = &stored
	return nil
}

func (r *memShareRepo) GetByID(shareID string) (*Share, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[shareID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	c := *s
	return &c, nil
}

func (r *memShareRepo) GetByDatabaseID(id int64) (*Share, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, share := range r.data {
		if share.ID == id {
			copy := *share
			return &copy, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *memShareRepo) ListOwnedByUser(ownerID string) ([]Share, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var out []Share
	for _, s := range r.data {
		if s.OwnerID == ownerID && (s.ExpiresAt == nil || s.ExpiresAt.After(now)) {
			c := *s
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *memShareRepo) ListForUser(targetUserID string) ([]Share, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var out []Share
	for _, s := range r.data {
		if s.TargetUserID == targetUserID && (s.ExpiresAt == nil || s.ExpiresAt.After(now)) {
			c := *s
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *memShareRepo) ListAsFiles(targetUserID string) ([]ShareFileView, error) {
	r.mu.Lock()
	now := time.Now()
	var out []ShareFileView
	for _, s := range r.data {
		if s.TargetUserID == targetUserID && (s.ExpiresAt == nil || s.ExpiresAt.After(now)) {
			c := *s
			view := ShareFileView{Share: c}
			// Look up owner username
			if u, ok := r.users.data[s.OwnerID]; ok {
				view.OwnerUsername = u.Username
			}
			out = append(out, view)
		}
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *memShareRepo) Delete(id int64, userID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for shareID, s := range r.data {
		if s.ID == id && (s.OwnerID == userID || s.TargetUserID == userID) {
			delete(r.data, shareID)
			return true, nil
		}
	}
	return false, nil
}

func (r *memShareRepo) UpdateFileSize(shareID string, newSize int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[shareID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	s.FileSize = newSize
	return nil
}

func (r *memShareRepo) SyncByInode(ownerID string, inode int64, filePath, fileName string, fileSize int64, contentType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, share := range r.data {
		if share.OwnerID == ownerID && share.FileInode == inode {
			share.FilePath, share.FileName, share.FileSize = filePath, fileName, fileSize
			share.ContentType = contentType
		}
	}
	return nil
}

func (r *memShareRepo) DeleteByInode(ownerID string, inode int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for shareID, share := range r.data {
		if share.OwnerID == ownerID && share.FileInode == inode {
			delete(r.data, shareID)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// memWorkspaceRepo
// ---------------------------------------------------------------------------

type memWorkspaceRepo struct {
	mu   sync.Mutex
	data map[string]string
}

func (r *memWorkspaceRepo) Save(userID, state string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[userID] = state
	return nil
}

func (r *memWorkspaceRepo) Get(userID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.data[userID]
	if !ok {
		return "", gorm.ErrRecordNotFound
	}
	return state, nil
}

func (r *memWorkspaceRepo) Delete(userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, userID)
	return nil
}

// ---------------------------------------------------------------------------
// memAuditRepo
// ---------------------------------------------------------------------------

type memAuditRepo struct{}

func (r *memAuditRepo) ListLogs(_ AuditFilter) ([]AuditLog, int64, error) {
	return []AuditLog{}, 0, nil
}

// ---------------------------------------------------------------------------
// memUserCleanupRepo
// ---------------------------------------------------------------------------

type memUserCleanupRepo struct {
	users     *memUserRepo
	files     *memFileRepo
	sessions  *memSessionRepo
	tasks     *memTaskRepo
	shares    *memShareRepo
	workspace *memWorkspaceRepo
}

func (r *memUserCleanupRepo) DeleteUserAndRelatedData(userID string) error {
	// Delete shares where user is owner or target
	r.shares.mu.Lock()
	for shareID, s := range r.shares.data {
		if s.OwnerID == userID || s.TargetUserID == userID {
			delete(r.shares.data, shareID)
		}
	}
	r.shares.mu.Unlock()

	// Delete files
	r.files.mu.Lock()
	for key, rec := range r.files.data {
		if rec.UserID == userID {
			delete(r.files.data, key)
		}
	}
	r.files.mu.Unlock()

	// Delete tasks
	r.tasks.mu.Lock()
	for taskID, t := range r.tasks.data {
		if t.UserID == userID {
			delete(r.tasks.data, taskID)
		}
	}
	r.tasks.mu.Unlock()

	// Delete workspace
	r.workspace.mu.Lock()
	delete(r.workspace.data, userID)
	r.workspace.mu.Unlock()

	// Delete sessions
	r.sessions.mu.Lock()
	for id, entry := range r.sessions.data {
		if entry.Session.UserID == userID {
			delete(r.sessions.data, id)
		}
	}
	r.sessions.mu.Unlock()

	// Delete user
	r.users.mu.Lock()
	delete(r.users.data, userID)
	r.users.mu.Unlock()

	return nil
}
