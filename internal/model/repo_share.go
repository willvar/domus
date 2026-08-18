package model

import "gorm.io/gorm"

// ShareRepo defines share data access operations.
type ShareRepo interface {
	Create(share *Share) error
	GetByID(shareID string) (*Share, error)
	GetByDatabaseID(id int64) (*Share, error)
	ListOwnedByUser(ownerID string) ([]Share, error)
	ListForUser(targetUserID string) ([]Share, error)
	ListAsFiles(targetUserID string) ([]ShareFileView, error)
	Delete(id int64, userID string) (bool, error)
	UpdateFileSize(shareID string, newSize int64) error
	SyncByInode(ownerID string, inode int64, filePath, fileName string, fileSize int64, contentType string) error
	DeleteByInode(ownerID string, inode int64) error
}

type gormShareRepo struct{ db *gorm.DB }

func (r *gormShareRepo) Create(share *Share) error {
	if share == nil || share.FileInode <= 0 {
		return ErrInvalidShareInode
	}
	return r.db.Create(share).Error
}

func (r *gormShareRepo) GetByID(shareID string) (*Share, error) {
	var s Share
	if err := r.db.Where("share_id = ?", shareID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *gormShareRepo) GetByDatabaseID(id int64) (*Share, error) {
	var share Share
	if err := r.db.Where("id = ?", id).First(&share).Error; err != nil {
		return nil, err
	}
	return &share, nil
}

func (r *gormShareRepo) ListOwnedByUser(ownerID string) ([]Share, error) {
	var shares []Share
	err := r.db.Where("owner_id = ? AND (expires_at IS NULL OR expires_at > NOW())", ownerID).
		Order("created_at DESC").Find(&shares).Error
	return shares, err
}

func (r *gormShareRepo) ListForUser(targetUserID string) ([]Share, error) {
	var shares []Share
	err := r.db.Where("target_user_id = ? AND (expires_at IS NULL OR expires_at > NOW())", targetUserID).
		Order("created_at DESC").Find(&shares).Error
	return shares, err
}

func (r *gormShareRepo) ListAsFiles(targetUserID string) ([]ShareFileView, error) {
	var views []ShareFileView
	err := r.db.Table("shares").
		Select("shares.*, users.username AS owner_username").
		Joins("JOIN users ON users.id = shares.owner_id").
		Where("shares.target_user_id = ? AND (shares.expires_at IS NULL OR shares.expires_at > NOW())", targetUserID).
		Order("shares.created_at DESC").
		Find(&views).Error
	return views, err
}

func (r *gormShareRepo) Delete(id int64, userID string) (bool, error) {
	res := r.db.Where("id = ? AND (owner_id = ? OR target_user_id = ?)", id, userID, userID).Delete(&Share{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *gormShareRepo) UpdateFileSize(shareID string, newSize int64) error {
	return r.db.Model(&Share{}).Where("share_id = ?", shareID).Update("file_size", newSize).Error
}

func (r *gormShareRepo) SyncByInode(ownerID string, inode int64, filePath, fileName string, fileSize int64, contentType string) error {
	if inode <= 0 {
		return nil
	}
	return r.db.Model(&Share{}).Where("owner_id = ? AND file_inode = ?", ownerID, inode).Updates(map[string]any{
		"file_path": filePath, "file_name": fileName, "file_size": fileSize, "content_type": contentType,
	}).Error
}

func (r *gormShareRepo) DeleteByInode(ownerID string, inode int64) error {
	if inode <= 0 {
		return nil
	}
	return r.db.Where("owner_id = ? AND file_inode = ?", ownerID, inode).Delete(&Share{}).Error
}
