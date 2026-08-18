package fileview

import (
	"errors"
	"time"

	"github.com/willvar/dofs"
	"gorm.io/gorm"

	"domus/internal/model"
)

func (r *Repo) CreateUpload(userID, uploadID, taskID, ossUploadID, physical, name string, fileSize int64, clientInstanceID string) error {
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return err
	}
	direct, err := r.runtime.Metadata.GetExternalUpload(ctx, userID, uploadID)
	if err != nil {
		return err
	}
	if direct.State != dofs.ExternalUploadActive || direct.Size != fileSize {
		return dofs.ErrConflict
	}
	parent, resolvedName, err := r.resolveParent(ctx, user, physical)
	if err != nil || parent.Inode == 0 || resolvedName != name {
		return dofs.ErrConflict
	}
	node, err := r.runtime.Metadata.GetNode(ctx, userID, direct.Inode)
	if err != nil || node.Parent != parent.Inode || node.Name != name {
		return dofs.ErrConflict
	}
	now := time.Now().UTC()
	return r.db.Create(&uploadRecord{
		ID: uploadID, UserID: userID, Inode: direct.Inode,
		ActorUserID: userID,
		Path:        physical, Parent: physicalParent(physical), Name: name, Size: fileSize,
		TaskID: taskID, OSSUploadID: ossUploadID, Status: "uploading",
		ClientInstanceID: clientInstanceID, LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error
}

func (r *Repo) BindUploadActor(ownerID, uploadID, actorUserID, shareID string) error {
	if ownerID == "" || uploadID == "" || actorUserID == "" || shareID == "" {
		return errors.New("invalid shared upload binding")
	}
	result := r.db.Model(&uploadRecord{}).
		Where("id = ? AND user_id = ? AND status = ?", uploadID, ownerID, "uploading").
		Updates(map[string]any{
			"actor_user_id": actorUserID,
			"share_id":      shareID,
			"updated_at":    time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// GetAuthorizedUpload resolves an upload by its initiating user. The returned
// FileRecord remains owned by UserID; shareID is non-empty only for a
// capability granted by a writable share.
func (r *Repo) GetAuthorizedUpload(actorUserID, uploadID string) (*model.FileRecord, string, error) {
	var upload uploadRecord
	if err := r.db.Where("id = ? AND actor_user_id = ?", uploadID, actorUserID).First(&upload).Error; err != nil {
		return nil, "", err
	}
	ctx, cancel := operationContext()
	defer cancel()
	owner, err := r.user(upload.UserID)
	if err != nil {
		return nil, "", err
	}
	record, err := r.recordFromUpload(ctx, owner, upload)
	return &record, upload.ShareID, err
}

func (r *Repo) GetUpload(userID, uploadID string) (*model.FileRecord, error) {
	var upload uploadRecord
	if err := r.db.Where("id = ? AND user_id = ?", uploadID, userID).First(&upload).Error; err != nil {
		return nil, err
	}
	ctx, cancel := operationContext()
	defer cancel()
	user, err := r.user(userID)
	if err != nil {
		return nil, err
	}
	record, err := r.recordFromUpload(ctx, user, upload)
	return &record, err
}

func (r *Repo) UpdateStatus(uploadID, status string) error {
	result := r.db.Model(&uploadRecord{}).Where("id = ?", uploadID).Updates(map[string]any{
		"status": status, "updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repo) TouchUpload(uploadID string, seenAt time.Time) error {
	result := r.db.Model(&uploadRecord{}).Where("id = ?", uploadID).Updates(map[string]any{
		"last_seen_at": seenAt, "updated_at": seenAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repo) uploadRecords(records []uploadRecord) ([]model.FileRecord, error) {
	result := make([]model.FileRecord, 0, len(records))
	ctx, cancel := operationContext()
	defer cancel()
	users := make(map[string]*model.User)
	for _, upload := range records {
		user := users[upload.UserID]
		if user == nil {
			var err error
			user, err = r.user(upload.UserID)
			if err != nil {
				return nil, err
			}
			users[upload.UserID] = user
		}
		record, err := r.recordFromUpload(ctx, user, upload)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (r *Repo) ListActiveUploads(userID string) ([]model.FileRecord, error) {
	var uploads []uploadRecord
	if err := r.db.Where("user_id = ? AND status = ?", userID, "uploading").Find(&uploads).Error; err != nil {
		return nil, err
	}
	return r.uploadRecords(uploads)
}

func (r *Repo) CancelUploadsForOtherInstances(userID, clientInstanceID string, cutoff time.Time) ([]model.FileRecord, error) {
	var uploads []uploadRecord
	query := r.db.Where("user_id = ? AND status = ? AND last_seen_at < ?", userID, "uploading", cutoff)
	if clientInstanceID != "" {
		query = query.Where("client_instance_id <> ?", clientInstanceID)
	}
	if err := query.Find(&uploads).Error; err != nil {
		return nil, err
	}
	if len(uploads) == 0 {
		return []model.FileRecord{}, nil
	}
	// The caller must abort the matching DOFS reservation and multipart upload
	// before the projection disappears. Marking rows cancelled here created a
	// crash window where sweepers could no longer discover leaked ciphertext.
	return r.uploadRecords(uploads)
}

func (r *Repo) GetStaleUploads(staleAfter time.Duration) ([]model.FileRecord, error) {
	var uploads []uploadRecord
	cutoff := time.Now().UTC().Add(-staleAfter)
	if err := r.db.Where("status = ? AND updated_at < ?", "uploading", cutoff).Find(&uploads).Error; err != nil {
		return nil, err
	}
	return r.uploadRecords(uploads)
}

func (r *Repo) deleteUpload(userID, uploadID string) error {
	result := r.db.Where("user_id = ? AND id = ?", userID, uploadID).Delete(&uploadRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("upload record not found")
	}
	return nil
}
