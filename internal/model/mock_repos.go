package model

import "time"

// MockRepos returns a Repos filled with all-default mock implementations.
func MockRepos() *Repos {
	return &Repos{
		Users:     &MockUserRepo{},
		Files:     &MockFileRepo{},
		Trash:     &MockTrashRepo{},
		Sessions:  &MockSessionRepo{},
		Jobs:      &MockJobRepo{},
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
	UpsertFn                  func(userID, path, name string, isDir bool, size int64, contentType, contentHash string, opts ...UpsertFileOpts) error
	GetFn                     func(userID, path string) (*FileRecord, error)
	DeleteFn                  func(userID, path string) error
	DeleteByPrefixFn          func(userID, prefix string) error
	ListByPrefixFn            func(userID, prefix string) ([]FileRecord, error)
	ListDirectChildrenFn      func(userID, parent string) ([]FileRecord, error)
	ListAllChildrenFn         func(userID, parent string) ([]FileRecord, error)
	MoveFn                    func(userID, oldPath, newPath, newName string) error
	MoveByPrefixFn            func(userID, oldPrefix, newPrefix string) error
	SumSizeByPrefixFn         func(userID, prefix string) (int64, error)
	UpdateThumbnailFn         func(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error
	UpdateSearchVectorFn      func(userID, path, text string) error
	RebuildAllSearchVectorsFn func() (int64, error)
	SearchFilesFn             func(userID, query string, limit int) ([]SearchFileResult, error)
	HasFullTextSearchFn       func() bool
	CreateUploadFn            func(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64) error
	GetUploadFn               func(userID, uploadID string) (*FileRecord, error)
	UpdateUploadPartsFn       func(uploadID, completedParts string) error
	UpdateStatusFn            func(uploadID, status string) error
	GetStaleUploadsFn         func(staleAfter time.Duration) ([]FileRecord, error)
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
func (m *MockFileRepo) SumSizeByPrefix(userID, prefix string) (int64, error) {
	if m.SumSizeByPrefixFn != nil {
		return m.SumSizeByPrefixFn(userID, prefix)
	}
	return 0, nil
}
func (m *MockFileRepo) UpdateThumbnail(userID, path, thumbnailKey, thumbnailWrappedDEK string, width, height int, duration float64) error {
	if m.UpdateThumbnailFn != nil {
		return m.UpdateThumbnailFn(userID, path, thumbnailKey, thumbnailWrappedDEK, width, height, duration)
	}
	return nil
}
func (m *MockFileRepo) UpdateSearchVector(userID, path, text string) error {
	if m.UpdateSearchVectorFn != nil {
		return m.UpdateSearchVectorFn(userID, path, text)
	}
	return nil
}
func (m *MockFileRepo) RebuildAllSearchVectors() (int64, error) {
	if m.RebuildAllSearchVectorsFn != nil {
		return m.RebuildAllSearchVectorsFn()
	}
	return 0, nil
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
func (m *MockFileRepo) CreateUpload(userID, uploadID, taskID, ossUploadID, path, name string, fileSize int64) error {
	if m.CreateUploadFn != nil {
		return m.CreateUploadFn(userID, uploadID, taskID, ossUploadID, path, name, fileSize)
	}
	return nil
}
func (m *MockFileRepo) GetUpload(userID, uploadID string) (*FileRecord, error) {
	if m.GetUploadFn != nil {
		return m.GetUploadFn(userID, uploadID)
	}
	return &FileRecord{UploadID: uploadID, Status: "uploading"}, nil
}
func (m *MockFileRepo) UpdateUploadParts(uploadID, completedParts string) error {
	if m.UpdateUploadPartsFn != nil {
		return m.UpdateUploadPartsFn(uploadID, completedParts)
	}
	return nil
}
func (m *MockFileRepo) UpdateStatus(uploadID, status string) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(uploadID, status)
	}
	return nil
}
func (m *MockFileRepo) GetStaleUploads(staleAfter time.Duration) ([]FileRecord, error) {
	if m.GetStaleUploadsFn != nil {
		return m.GetStaleUploadsFn(staleAfter)
	}
	return []FileRecord{}, nil
}

// --- MockTrashRepo ---

type MockTrashRepo struct {
	CreateFn func(userID, originalPath, trashKey string, size int64, isDir bool) error
	ListFn   func(userID string) ([]TrashItem, error)
	GetFn    func(id int64, userID string) (*TrashItem, error)
	DeleteFn func(id int64) error
	ClearFn  func(userID string) ([]TrashItem, error)
}

func (m *MockTrashRepo) Create(userID, originalPath, trashKey string, size int64, isDir bool) error {
	if m.CreateFn != nil {
		return m.CreateFn(userID, originalPath, trashKey, size, isDir)
	}
	return nil
}
func (m *MockTrashRepo) List(userID string) ([]TrashItem, error) {
	if m.ListFn != nil {
		return m.ListFn(userID)
	}
	return []TrashItem{}, nil
}
func (m *MockTrashRepo) Get(id int64, userID string) (*TrashItem, error) {
	if m.GetFn != nil {
		return m.GetFn(id, userID)
	}
	return &TrashItem{ID: id, UserID: userID}, nil
}
func (m *MockTrashRepo) Delete(id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(id)
	}
	return nil
}
func (m *MockTrashRepo) Clear(userID string) ([]TrashItem, error) {
	if m.ClearFn != nil {
		return m.ClearFn(userID)
	}
	return []TrashItem{}, nil
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

// --- MockJobRepo ---

type MockJobRepo struct {
	CreateFn            func(userID, jobID, jobType, params string) (*Job, error)
	CreateDirectFn      func(job *Job) error
	GetByJobIDFn        func(jobID string) (*Job, error)
	ListActiveFn        func(userID string) ([]Job, error)
	ListActiveUploadsFn func(userID string) ([]Job, error)
	ListRecentFn        func(userID string) ([]Job, error)
	DeleteCompletedFn   func(userID string) error
	UpdateStatusFn      func(jobID, status string) error
	UpdateProgressFn    func(jobID string, progress float64, phase string) error
	UpdateResultFn      func(jobID, result string) error
	UpdateErrorFn       func(jobID, errorMsg string) error
	ClaimPendingFn      func(jobType string) (*Job, error)
	FindActiveByParamFn func(jobType, paramSubstr string) (*Job, error)
	ResetRunningFn      func() error
	FindByTaskIDFn      func(taskID string) ([]Job, error)
}

func (m *MockJobRepo) Create(userID, jobID, jobType, params string) (*Job, error) {
	if m.CreateFn != nil {
		return m.CreateFn(userID, jobID, jobType, params)
	}
	return &Job{JobID: jobID, Type: jobType, Status: "pending"}, nil
}
func (m *MockJobRepo) CreateDirect(job *Job) error {
	if m.CreateDirectFn != nil {
		return m.CreateDirectFn(job)
	}
	return nil
}
func (m *MockJobRepo) GetByJobID(jobID string) (*Job, error) {
	if m.GetByJobIDFn != nil {
		return m.GetByJobIDFn(jobID)
	}
	return &Job{JobID: jobID, Status: "pending"}, nil
}
func (m *MockJobRepo) ListActive(userID string) ([]Job, error) {
	if m.ListActiveFn != nil {
		return m.ListActiveFn(userID)
	}
	return []Job{}, nil
}
func (m *MockJobRepo) ListActiveUploads(userID string) ([]Job, error) {
	if m.ListActiveUploadsFn != nil {
		return m.ListActiveUploadsFn(userID)
	}
	return []Job{}, nil
}
func (m *MockJobRepo) ListRecent(userID string) ([]Job, error) {
	if m.ListRecentFn != nil {
		return m.ListRecentFn(userID)
	}
	return []Job{}, nil
}
func (m *MockJobRepo) DeleteCompleted(userID string) error {
	if m.DeleteCompletedFn != nil {
		return m.DeleteCompletedFn(userID)
	}
	return nil
}
func (m *MockJobRepo) UpdateStatus(jobID, status string) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(jobID, status)
	}
	return nil
}
func (m *MockJobRepo) UpdateProgress(jobID string, progress float64, phase string) error {
	if m.UpdateProgressFn != nil {
		return m.UpdateProgressFn(jobID, progress, phase)
	}
	return nil
}
func (m *MockJobRepo) UpdateResult(jobID, result string) error {
	if m.UpdateResultFn != nil {
		return m.UpdateResultFn(jobID, result)
	}
	return nil
}
func (m *MockJobRepo) UpdateError(jobID, errorMsg string) error {
	if m.UpdateErrorFn != nil {
		return m.UpdateErrorFn(jobID, errorMsg)
	}
	return nil
}
func (m *MockJobRepo) ClaimPending(jobType string) (*Job, error) {
	if m.ClaimPendingFn != nil {
		return m.ClaimPendingFn(jobType)
	}
	return nil, nil
}
func (m *MockJobRepo) FindActiveByParam(jobType, paramSubstr string) (*Job, error) {
	if m.FindActiveByParamFn != nil {
		return m.FindActiveByParamFn(jobType, paramSubstr)
	}
	return nil, nil
}
func (m *MockJobRepo) ResetRunning() error {
	if m.ResetRunningFn != nil {
		return m.ResetRunningFn()
	}
	return nil
}
func (m *MockJobRepo) FindByTaskID(taskID string) ([]Job, error) {
	if m.FindByTaskIDFn != nil {
		return m.FindByTaskIDFn(taskID)
	}
	return []Job{}, nil
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
	CreateFn         func(share *Share) error
	GetByIDFn        func(shareID string) (*Share, error)
	ListForFileFn    func(ownerID, filePath string) ([]Share, error)
	ListForUserFn    func(targetUserID string) ([]Share, error)
	ListAsFilesFn    func(targetUserID string) ([]ShareFileView, error)
	DeleteFn         func(id int64, userID string) (bool, error)
	UpdateFileSizeFn func(shareID string, newSize int64) error
	DeleteByPathFn   func(ownerID, filePath string) error
	DeleteByPrefixFn func(ownerID, prefix string) error
	MoveByPathFn     func(ownerID, oldPath, newPath string) error
	MoveByPrefixFn   func(ownerID, oldPrefix, newPrefix string) error
	GetForUserFn     func(filePath, targetUserID string) (*Share, error)
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
