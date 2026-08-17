package model

import (
	"io"
	"time"

	"gorm.io/gorm"
)

// MockRepos returns a Repos filled with all-default mock implementations.
func MockRepos() *Repos {
	return &Repos{
		Users:     &MockUserRepo{},
		Files:     &MockFileRepo{},
		Sessions:  &MockSessionRepo{},
		Tasks:     &MockTaskRepo{},
		Audit:     &MockAuditRepo{},
		Shares:    &MockShareRepo{},
		Workspace: &MockWorkspaceRepo{},
		Cleanup:   &MockUserCleanupRepo{},
	}
}

// --- MockUserRepo ---

type MockUserRepo struct {
	CreateFn            func(username, password, role, wrappedKEK string) (*User, error)
	GetByIDFn           func(id string) (*User, error)
	GetByUsernameFn     func(username string) (*User, error)
	ListFn              func() ([]User, error)
	UpdateRoleFn        func(id, role string) error
	UpdatePasswordFn    func(id, password string) error
	UpdateEmailFn       func(id, email string) error
	UpdateTOTPFn        func(id, secret string, enabled bool) error
	UpdateDisplayNameFn func(id, displayName string) error
	DeleteFn            func(id string) error
	CountFn             func() (int, error)
	CountByRoleFn       func(role string) (int, error)
	GetWrappedKEKFn     func(userID string) (string, error)
	SetWrappedKEKFn     func(userID, wrappedKEK string) error
}

func (m *MockUserRepo) Create(username, password, role, wrappedKEK string) (*User, error) {
	if m.CreateFn != nil {
		return m.CreateFn(username, password, role, wrappedKEK)
	}
	return &User{ID: "mock-user-id", Username: username, Role: role}, nil
}
func (m *MockUserRepo) GetByID(id string) (*User, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(id)
	}
	return &User{ID: id, Username: "mock-user"}, nil
}
func (m *MockUserRepo) GetByUsername(username string) (*User, error) {
	if m.GetByUsernameFn != nil {
		return m.GetByUsernameFn(username)
	}
	return &User{ID: "mock-user-id", Username: username}, nil
}
func (m *MockUserRepo) List() ([]User, error) {
	if m.ListFn != nil {
		return m.ListFn()
	}
	return []User{}, nil
}
func (m *MockUserRepo) UpdateRole(id, role string) error {
	if m.UpdateRoleFn != nil {
		return m.UpdateRoleFn(id, role)
	}
	return nil
}
func (m *MockUserRepo) UpdatePassword(id, password string) error {
	if m.UpdatePasswordFn != nil {
		return m.UpdatePasswordFn(id, password)
	}
	return nil
}
func (m *MockUserRepo) UpdateEmail(id, email string) error {
	if m.UpdateEmailFn != nil {
		return m.UpdateEmailFn(id, email)
	}
	return nil
}
func (m *MockUserRepo) UpdateTOTP(id, secret string, enabled bool) error {
	if m.UpdateTOTPFn != nil {
		return m.UpdateTOTPFn(id, secret, enabled)
	}
	return nil
}
func (m *MockUserRepo) UpdateDisplayName(id, displayName string) error {
	if m.UpdateDisplayNameFn != nil {
		return m.UpdateDisplayNameFn(id, displayName)
	}
	return nil
}
func (m *MockUserRepo) Delete(id string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(id)
	}
	return nil
}
func (m *MockUserRepo) Count() (int, error) {
	if m.CountFn != nil {
		return m.CountFn()
	}
	return 0, nil
}
func (m *MockUserRepo) CountByRole(role string) (int, error) {
	if m.CountByRoleFn != nil {
		return m.CountByRoleFn(role)
	}
	return 0, nil
}
func (m *MockUserRepo) GetWrappedKEK(userID string) (string, error) {
	if m.GetWrappedKEKFn != nil {
		return m.GetWrappedKEKFn(userID)
	}
	return "", nil
}
func (m *MockUserRepo) SetWrappedKEK(userID, wrappedKEK string) error {
	if m.SetWrappedKEKFn != nil {
		return m.SetWrappedKEKFn(userID, wrappedKEK)
	}
	return nil
}

// --- MockFileRepo ---

type MockFileRepo struct {
	UpsertFn                         func(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error
	GetFn                            func(userID, path string) (*FileRecord, error)
	GetByIDFn                        func(userID string, id int64) (*FileRecord, error)
	GetByStorageKeyFn                func(userID, objectKey string) (*FileRecord, error)
	CommitGenerationFn               func(userID string, id, expectedGeneration int64, objectKey string, size int64) (bool, error)
	CreateDOFSNodeFn                 func(userID, path, name, wrappedDEK string, isDir bool) (*FileRecord, error)
	FinalizeDOFSFileFn               func(userID string, id int64, objectKey string) (bool, error)
	AbortDOFSFileFn                  func(userID string, id int64) error
	RemoveDOFSNodeFn                 func(userID, path string, isDir bool) (*FileRecord, error)
	RenameDOFSNodeFn                 func(userID, oldPath, newPath, newName string, isDir, replace bool) (*FileRecord, *FileRecord, error)
	ListCreatingDOFSNodesFn          func(userID string) ([]FileRecord, error)
	ListDeletedDOFSNodesFn           func(userID string) ([]FileRecord, error)
	PurgeDeletedDOFSNodeFn           func(userID string, id int64) (bool, error)
	AcquireDOFSMountLeaseFn          func(userID string) (io.Closer, error)
	DeleteFn                         func(userID, path string) error
	DeleteByPrefixFn                 func(userID, prefix string) error
	ListByPrefixFn                   func(userID, prefix string) ([]FileRecord, error)
	ListDirectChildrenFn             func(userID, parent string) ([]FileRecord, error)
	ListAllChildrenFn                func(userID, parent string) ([]FileRecord, error)
	MoveFn                           func(userID, oldPath, newPath, newName string) error
	MoveByPrefixFn                   func(userID, oldPrefix, newPrefix string) error
	UpdateThumbnailFn                func(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error
	UpdateThumbnailIfGenerationFn    func(userID, path string, fileID, generation int64, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) (bool, error)
	UpdateContentTypeFn              func(userID, path, contentType string) error
	UpdateSearchVectorFn             func(userID, path, text string) error
	SearchFilesFn                    func(userID, query string, limit int) ([]SearchFileResult, error)
	HasFullTextSearchFn              func() bool
	CreateUploadFn                   func(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64, clientInstanceID string) error
	GetUploadFn                      func(userID, uploadID string) (*FileRecord, error)
	UpdateStatusFn                   func(uploadID, status string) error
	TouchUploadFn                    func(uploadID string, seenAt time.Time) error
	ListActiveUploadsFn              func(userID string) ([]FileRecord, error)
	CancelUploadsForOtherInstancesFn func(userID, clientInstanceID string, cutoff time.Time) ([]FileRecord, error)
	GetStaleUploadsFn                func(staleAfter time.Duration) ([]FileRecord, error)
}

func (m *MockFileRepo) Upsert(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error {
	if m.UpsertFn != nil {
		return m.UpsertFn(userID, path, name, isDir, size, contentType, contentHash, opts...)
	}
	return nil
}
func (m *MockFileRepo) Get(userID, path string) (*FileRecord, error) {
	if m.GetFn != nil {
		return m.GetFn(userID, path)
	}
	return &FileRecord{UserID: userID, Path: path, Name: path, Status: "ready"}, nil
}
func (m *MockFileRepo) GetByID(userID string, id int64) (*FileRecord, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(userID, id)
	}
	return &FileRecord{ID: id, UserID: userID, Status: "ready"}, nil
}
func (m *MockFileRepo) GetByStorageKey(userID, objectKey string) (*FileRecord, error) {
	if m.GetByStorageKeyFn != nil {
		return m.GetByStorageKeyFn(userID, objectKey)
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *MockFileRepo) CommitGeneration(userID string, id, expectedGeneration int64, objectKey string, size int64) (bool, error) {
	if m.CommitGenerationFn != nil {
		return m.CommitGenerationFn(userID, id, expectedGeneration, objectKey, size)
	}
	return true, nil
}
func (m *MockFileRepo) CreateDOFSNode(userID, path, name, wrappedDEK string, isDir bool) (*FileRecord, error) {
	if m.CreateDOFSNodeFn != nil {
		return m.CreateDOFSNodeFn(userID, path, name, wrappedDEK, isDir)
	}
	status := "creating"
	if isDir {
		status = "ready"
	}
	return &FileRecord{ID: 1, UserID: userID, Path: path, Name: name, IsDir: isDir, WrappedDEK: wrappedDEK, Status: status}, nil
}
func (m *MockFileRepo) FinalizeDOFSFile(userID string, id int64, objectKey string) (bool, error) {
	if m.FinalizeDOFSFileFn != nil {
		return m.FinalizeDOFSFileFn(userID, id, objectKey)
	}
	return true, nil
}
func (m *MockFileRepo) AbortDOFSFile(userID string, id int64) error {
	if m.AbortDOFSFileFn != nil {
		return m.AbortDOFSFileFn(userID, id)
	}
	return nil
}
func (m *MockFileRepo) RemoveDOFSNode(userID, path string, isDir bool) (*FileRecord, error) {
	if m.RemoveDOFSNodeFn != nil {
		return m.RemoveDOFSNodeFn(userID, path, isDir)
	}
	return &FileRecord{ID: 1, UserID: userID, Path: path, IsDir: isDir, Status: "deleted"}, nil
}
func (m *MockFileRepo) RenameDOFSNode(userID, oldPath, newPath, newName string, isDir, replace bool) (*FileRecord, *FileRecord, error) {
	if m.RenameDOFSNodeFn != nil {
		return m.RenameDOFSNodeFn(userID, oldPath, newPath, newName, isDir, replace)
	}
	return &FileRecord{ID: 1, UserID: userID, Path: newPath, Name: newName, IsDir: isDir, Status: "ready"}, nil, nil
}
func (m *MockFileRepo) ListCreatingDOFSNodes(userID string) ([]FileRecord, error) {
	if m.ListCreatingDOFSNodesFn != nil {
		return m.ListCreatingDOFSNodesFn(userID)
	}
	return nil, nil
}
func (m *MockFileRepo) ListDeletedDOFSNodes(userID string) ([]FileRecord, error) {
	if m.ListDeletedDOFSNodesFn != nil {
		return m.ListDeletedDOFSNodesFn(userID)
	}
	return nil, nil
}
func (m *MockFileRepo) PurgeDeletedDOFSNode(userID string, id int64) (bool, error) {
	if m.PurgeDeletedDOFSNodeFn != nil {
		return m.PurgeDeletedDOFSNodeFn(userID, id)
	}
	return true, nil
}

type mockDOFSLease struct{}

func (mockDOFSLease) Close() error { return nil }

func (m *MockFileRepo) AcquireDOFSMountLease(userID string) (io.Closer, error) {
	if m.AcquireDOFSMountLeaseFn != nil {
		return m.AcquireDOFSMountLeaseFn(userID)
	}
	return mockDOFSLease{}, nil
}
func (m *MockFileRepo) Delete(userID, path string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(userID, path)
	}
	return nil
}
func (m *MockFileRepo) DeleteByPrefix(userID, prefix string) error {
	if m.DeleteByPrefixFn != nil {
		return m.DeleteByPrefixFn(userID, prefix)
	}
	return nil
}
func (m *MockFileRepo) ListByPrefix(userID, prefix string) ([]FileRecord, error) {
	if m.ListByPrefixFn != nil {
		return m.ListByPrefixFn(userID, prefix)
	}
	return []FileRecord{}, nil
}
func (m *MockFileRepo) ListDirectChildren(userID, parent string) ([]FileRecord, error) {
	if m.ListDirectChildrenFn != nil {
		return m.ListDirectChildrenFn(userID, parent)
	}
	return []FileRecord{}, nil
}
func (m *MockFileRepo) ListAllChildren(userID, parent string) ([]FileRecord, error) {
	if m.ListAllChildrenFn != nil {
		return m.ListAllChildrenFn(userID, parent)
	}
	return []FileRecord{}, nil
}
func (m *MockFileRepo) Move(userID, oldPath, newPath, newName string) error {
	if m.MoveFn != nil {
		return m.MoveFn(userID, oldPath, newPath, newName)
	}
	return nil
}
func (m *MockFileRepo) MoveByPrefix(userID, oldPrefix, newPrefix string) error {
	if m.MoveByPrefixFn != nil {
		return m.MoveByPrefixFn(userID, oldPrefix, newPrefix)
	}
	return nil
}
func (m *MockFileRepo) UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	if m.UpdateThumbnailFn != nil {
		return m.UpdateThumbnailFn(userID, path, thumbnailKey, thumbnailWrappedDEK, width, height, duration)
	}
	return nil
}

func (m *MockFileRepo) UpdateThumbnailIfGeneration(userID, path string, fileID, generation int64, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) (bool, error) {
	if m.UpdateThumbnailIfGenerationFn != nil {
		return m.UpdateThumbnailIfGenerationFn(userID, path, fileID, generation, thumbnailKey, thumbnailWrappedDEK, width, height, duration)
	}
	return true, nil
}

func (m *MockFileRepo) UpdateContentType(userID, path, contentType string) error {
	if m.UpdateContentTypeFn != nil {
		return m.UpdateContentTypeFn(userID, path, contentType)
	}
	return nil
}
func (m *MockFileRepo) UpdateSearchVector(userID, path, text string) error {
	if m.UpdateSearchVectorFn != nil {
		return m.UpdateSearchVectorFn(userID, path, text)
	}
	return nil
}
func (m *MockFileRepo) SearchFiles(userID, query string, limit int) ([]SearchFileResult, error) {
	if m.SearchFilesFn != nil {
		return m.SearchFilesFn(userID, query, limit)
	}
	return []SearchFileResult{}, nil
}
func (m *MockFileRepo) HasFullTextSearch() bool {
	if m.HasFullTextSearchFn != nil {
		return m.HasFullTextSearchFn()
	}
	return false
}
func (m *MockFileRepo) CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64, clientInstanceID string) error {
	if m.CreateUploadFn != nil {
		return m.CreateUploadFn(userID, uploadID, taskID, ossUploadID, path, name, fileSize, clientInstanceID)
	}
	return nil
}
func (m *MockFileRepo) GetUpload(userID, uploadID string) (*FileRecord, error) {
	if m.GetUploadFn != nil {
		return m.GetUploadFn(userID, uploadID)
	}
	return &FileRecord{UploadID: uploadID, Status: "uploading"}, nil
}
func (m *MockFileRepo) UpdateStatus(uploadID, status string) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(uploadID, status)
	}
	return nil
}
func (m *MockFileRepo) TouchUpload(uploadID string, seenAt time.Time) error {
	if m.TouchUploadFn != nil {
		return m.TouchUploadFn(uploadID, seenAt)
	}
	return nil
}
func (m *MockFileRepo) ListActiveUploads(userID string) ([]FileRecord, error) {
	if m.ListActiveUploadsFn != nil {
		return m.ListActiveUploadsFn(userID)
	}
	return []FileRecord{}, nil
}
func (m *MockFileRepo) CancelUploadsForOtherInstances(userID, clientInstanceID string, cutoff time.Time) ([]FileRecord, error) {
	if m.CancelUploadsForOtherInstancesFn != nil {
		return m.CancelUploadsForOtherInstancesFn(userID, clientInstanceID, cutoff)
	}
	return []FileRecord{}, nil
}
func (m *MockFileRepo) GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error) {
	if m.GetStaleUploadsFn != nil {
		return m.GetStaleUploadsFn(staleAfter)
	}
	return []FileRecord{}, nil
}

// --- MockSessionRepo ---

type MockSessionRepo struct {
	CreateFn               func(userID, username, role string) (string, error)
	GetFn                  func(id string) *Session
	DeleteFn               func(id string)
	DeleteByUserIDFn       func(userID string)
	DeleteByUserIDExceptFn func(userID, exceptSessionID string)
	CleanExpiredFn         func()
	SetPopulateKEKFn       func(fn func(*Session))
}

func (m *MockSessionRepo) Create(userID, username, role string) (string, error) {
	if m.CreateFn != nil {
		return m.CreateFn(userID, username, role)
	}
	return "mock-session-id", nil
}
func (m *MockSessionRepo) Get(id string) *Session {
	if m.GetFn != nil {
		return m.GetFn(id)
	}
	return nil
}
func (m *MockSessionRepo) Delete(id string) {
	if m.DeleteFn != nil {
		m.DeleteFn(id)
	}
}
func (m *MockSessionRepo) DeleteByUserID(userID string) {
	if m.DeleteByUserIDFn != nil {
		m.DeleteByUserIDFn(userID)
	}
}
func (m *MockSessionRepo) DeleteByUserIDExcept(userID, exceptSessionID string) {
	if m.DeleteByUserIDExceptFn != nil {
		m.DeleteByUserIDExceptFn(userID, exceptSessionID)
	}
}
func (m *MockSessionRepo) CleanExpired() {
	if m.CleanExpiredFn != nil {
		m.CleanExpiredFn()
	}
}
func (m *MockSessionRepo) SetPopulateKEK(fn func(*Session)) {
	if m.SetPopulateKEKFn != nil {
		m.SetPopulateKEKFn(fn)
	}
}

// --- MockTaskRepo ---

type MockTaskRepo struct {
	CreateFn          func(userID, taskID, taskType, name string) error
	GetFn             func(taskID string) (*Task, error)
	UpdateProgressFn  func(taskID string, progress float64, phase string) error
	UpdateStatusFn    func(taskID, status string) error
	ListRecentFn      func(userID string) ([]Task, error)
	DeleteCompletedFn func(userID string) error
	DeleteFn          func(taskID string) error
}

func (m *MockTaskRepo) Create(userID, taskID, taskType, name string) error {
	if m.CreateFn != nil {
		return m.CreateFn(userID, taskID, taskType, name)
	}
	return nil
}
func (m *MockTaskRepo) Get(taskID string) (*Task, error) {
	if m.GetFn != nil {
		return m.GetFn(taskID)
	}
	return &Task{TaskID: taskID, Status: "running"}, nil
}
func (m *MockTaskRepo) UpdateProgress(taskID string, progress float64, phase string) error {
	if m.UpdateProgressFn != nil {
		return m.UpdateProgressFn(taskID, progress, phase)
	}
	return nil
}
func (m *MockTaskRepo) UpdateStatus(taskID, status string) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(taskID, status)
	}
	return nil
}
func (m *MockTaskRepo) ListRecent(userID string) ([]Task, error) {
	if m.ListRecentFn != nil {
		return m.ListRecentFn(userID)
	}
	return []Task{}, nil
}
func (m *MockTaskRepo) DeleteCompleted(userID string) error {
	if m.DeleteCompletedFn != nil {
		return m.DeleteCompletedFn(userID)
	}
	return nil
}
func (m *MockTaskRepo) Delete(taskID string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(taskID)
	}
	return nil
}

// --- MockAuditRepo ---

type MockAuditRepo struct {
	ListLogsFn func(filter AuditFilter) ([]AuditLog, int64, error)
}

func (m *MockAuditRepo) ListLogs(filter AuditFilter) ([]AuditLog, int64, error) {
	if m.ListLogsFn != nil {
		return m.ListLogsFn(filter)
	}
	return []AuditLog{}, 0, nil
}

// --- MockShareRepo ---

type MockShareRepo struct {
	CreateFn          func(share *Share) error
	GetByIDFn         func(shareID string) (*Share, error)
	ListForFileFn     func(ownerID, filePath string) ([]Share, error)
	ListOwnedByUserFn func(ownerID string) ([]Share, error)
	ListForUserFn     func(targetUserID string) ([]Share, error)
	ListAsFilesFn     func(targetUserID string) ([]ShareFileView, error)
	DeleteFn          func(id int64, userID string) (bool, error)
	UpdateFileSizeFn  func(shareID string, newSize int64) error
	DeleteByPathFn    func(ownerID, filePath string) error
	DeleteByPrefixFn  func(ownerID, prefix string) error
	MoveByPathFn      func(ownerID, oldPath, newPath string) error
	MoveByPrefixFn    func(ownerID, oldPrefix, newPrefix string) error
	GetForUserFn      func(filePath, targetUserID string) (*Share, error)
}

func (m *MockShareRepo) Create(share *Share) error {
	if m.CreateFn != nil {
		return m.CreateFn(share)
	}
	return nil
}
func (m *MockShareRepo) GetByID(shareID string) (*Share, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(shareID)
	}
	return &Share{ShareID: shareID}, nil
}
func (m *MockShareRepo) ListForFile(ownerID, filePath string) ([]Share, error) {
	if m.ListForFileFn != nil {
		return m.ListForFileFn(ownerID, filePath)
	}
	return []Share{}, nil
}
func (m *MockShareRepo) ListOwnedByUser(ownerID string) ([]Share, error) {
	if m.ListOwnedByUserFn != nil {
		return m.ListOwnedByUserFn(ownerID)
	}
	return []Share{}, nil
}
func (m *MockShareRepo) ListForUser(targetUserID string) ([]Share, error) {
	if m.ListForUserFn != nil {
		return m.ListForUserFn(targetUserID)
	}
	return []Share{}, nil
}
func (m *MockShareRepo) ListAsFiles(targetUserID string) ([]ShareFileView, error) {
	if m.ListAsFilesFn != nil {
		return m.ListAsFilesFn(targetUserID)
	}
	return []ShareFileView{}, nil
}
func (m *MockShareRepo) Delete(id int64, userID string) (bool, error) {
	if m.DeleteFn != nil {
		return m.DeleteFn(id, userID)
	}
	return true, nil
}
func (m *MockShareRepo) UpdateFileSize(shareID string, newSize int64) error {
	if m.UpdateFileSizeFn != nil {
		return m.UpdateFileSizeFn(shareID, newSize)
	}
	return nil
}
func (m *MockShareRepo) DeleteByPath(ownerID, filePath string) error {
	if m.DeleteByPathFn != nil {
		return m.DeleteByPathFn(ownerID, filePath)
	}
	return nil
}
func (m *MockShareRepo) DeleteByPrefix(ownerID, prefix string) error {
	if m.DeleteByPrefixFn != nil {
		return m.DeleteByPrefixFn(ownerID, prefix)
	}
	return nil
}
func (m *MockShareRepo) MoveByPath(ownerID, oldPath, newPath string) error {
	if m.MoveByPathFn != nil {
		return m.MoveByPathFn(ownerID, oldPath, newPath)
	}
	return nil
}
func (m *MockShareRepo) MoveByPrefix(ownerID, oldPrefix, newPrefix string) error {
	if m.MoveByPrefixFn != nil {
		return m.MoveByPrefixFn(ownerID, oldPrefix, newPrefix)
	}
	return nil
}
func (m *MockShareRepo) GetForUser(filePath, targetUserID string) (*Share, error) {
	if m.GetForUserFn != nil {
		return m.GetForUserFn(filePath, targetUserID)
	}
	return nil, nil
}

// --- MockWorkspaceRepo ---

type MockWorkspaceRepo struct {
	SaveFn   func(userID, state string) error
	GetFn    func(userID string) (string, error)
	DeleteFn func(userID string) error
}

func (m *MockWorkspaceRepo) Save(userID, state string) error {
	if m.SaveFn != nil {
		return m.SaveFn(userID, state)
	}
	return nil
}
func (m *MockWorkspaceRepo) Get(userID string) (string, error) {
	if m.GetFn != nil {
		return m.GetFn(userID)
	}
	return "{}", nil
}
func (m *MockWorkspaceRepo) Delete(userID string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(userID)
	}
	return nil
}

// --- MockUserCleanupRepo ---

type MockUserCleanupRepo struct {
	DeleteUserAndRelatedDataFn func(userID string) error
}

func (m *MockUserCleanupRepo) DeleteUserAndRelatedData(userID string) error {
	if m.DeleteUserAndRelatedDataFn != nil {
		return m.DeleteUserAndRelatedDataFn(userID)
	}
	return nil
}
