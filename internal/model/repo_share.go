package model

import (
	"path/filepath"

	"gorm.io/gorm"
)

// ShareRepo defines share data access operations.
type ShareRepo interface {
	Create(share *Share) error
	GetByID(shareID string) (*Share, error)
	ListForFile(ownerID, filePath string) ([]Share, error)
	ListForUser(targetUserID string) ([]Share, error)
	ListAsFiles(targetUserID string) ([]ShareFileView, error)
	Delete(id int64, userID string) (bool, error)
	UpdateFileSize(shareID string, newSize int64) error
	DeleteByPath(ownerID, filePath string) error
	DeleteByPrefix(ownerID, prefix string) error
	MoveByPath(ownerID, oldPath, newPath string) error
	MoveByPrefix(ownerID, oldPrefix, newPrefix string) error
	GetForUser(filePath, targetUserID string) (*Share, error)
}

type gormShareRepo struct{ db *gorm.DB }

func (r *gormShareRepo) Create(share *Share) error {
	return r.db.Create(share).Error
}

func (r *gormShareRepo) GetByID(shareID string) (*Share, error) {
	var s Share
	if err := r.db.Where("share_id = ?", shareID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *gormShareRepo) ListForFile(ownerID, filePath string) ([]Share, error) {
	var shares []Share
	err := r.db.Where("owner_id = ? AND file_path = ?", ownerID, filePath).
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

func (r *gormShareRepo) DeleteByPath(ownerID, filePath string) error {
	return r.db.Where("owner_id = ? AND file_path = ?", ownerID, filePath).Delete(&Share{}).Error
}

func (r *gormShareRepo) DeleteByPrefix(ownerID, prefix string) error {
	return r.db.Where("owner_id = ? AND file_path LIKE ?", ownerID, prefix+"%").Delete(&Share{}).Error
}

func (r *gormShareRepo) MoveByPath(ownerID, oldPath, newPath string) error {
	return r.db.Model(&Share{}).
		Where("owner_id = ? AND file_path = ?", ownerID, oldPath).
		Updates(map[string]interface{}{
			"file_path": newPath,
			"file_name": filepath.Base(newPath),
		}).Error
}

func (r *gormShareRepo) MoveByPrefix(ownerID, oldPrefix, newPrefix string) error {
	return r.db.Model(&Share{}).
		Where("owner_id = ? AND file_path LIKE ?", ownerID, oldPrefix+"%").
		Updates(map[string]interface{}{
			"file_path": gorm.Expr("REPLACE(file_path, ?, ?)", oldPrefix, newPrefix),
		}).Error
}

func (r *gormShareRepo) GetForUser(filePath, targetUserID string) (*Share, error) {
	var s Share
	if err := r.db.Where("file_path = ? AND target_user_id = ?", filePath, targetUserID).
		First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}
