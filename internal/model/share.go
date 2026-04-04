package model

import (
	"path/filepath"
	"time"

	"gorm.io/gorm"
)

// Share represents a user-to-user file share.
type Share struct {
	ID           int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	ShareID      string     `gorm:"uniqueIndex;not null" json:"share_id"`
	OwnerID      string     `gorm:"not null;index" json:"owner_id"`
	FilePath     string     `gorm:"not null" json:"file_path"`
	FileName     string     `gorm:"not null" json:"file_name"`
	FileSize     int64      `gorm:"not null;default:0" json:"file_size"`
	ContentType  string     `gorm:"default:''" json:"content_type"`
	TargetUserID string     `gorm:"not null;index" json:"target_user_id"`
	WrappedDEK   string     `gorm:"not null" json:"-"`                         // DEK wrapped with target user's KEK
	Permission   string     `gorm:"not null;default:'read'" json:"permission"` // "read" or "write"
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (Share) TableName() string { return "shares" }

// CreateShare inserts a new share record.
func CreateShare(share *Share) error {
	return db.Create(share).Error
}

// GetShareByID retrieves a share by its ShareID (UUID).
func GetShareByID(shareID string) (*Share, error) {
	var s Share
	if err := db.Where("share_id = ?", shareID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSharesForFile returns all shares for a given file path owned by the user.
func ListSharesForFile(ownerID, filePath string) ([]Share, error) {
	var shares []Share
	err := db.Where("owner_id = ? AND file_path = ?", ownerID, filePath).
		Order("created_at DESC").Find(&shares).Error
	return shares, err
}

// ListSharesForUser returns all non-expired files shared with a specific user.
func ListSharesForUser(targetUserID string) ([]Share, error) {
	var shares []Share
	err := db.Where("target_user_id = ? AND (expires_at IS NULL OR expires_at > NOW())", targetUserID).
		Order("created_at DESC").Find(&shares).Error
	return shares, err
}

// ShareFileView is a Share record augmented with the owner's username for display.
type ShareFileView struct {
	Share
	OwnerUsername string `json:"owner_username"`
}

// ListSharesAsFiles returns shares for a user with the owner's username, for Dolphin listing.
func ListSharesAsFiles(targetUserID string) ([]ShareFileView, error) {
	var views []ShareFileView
	err := db.Table("shares").
		Select("shares.*, users.username AS owner_username").
		Joins("JOIN users ON users.id = shares.owner_id").
		Where("shares.target_user_id = ? AND (shares.expires_at IS NULL OR shares.expires_at > NOW())", targetUserID).
		Order("shares.created_at DESC").
		Find(&views).Error
	return views, err
}

// DeleteShare removes a share by ID, scoped to the owner or target user.
// Returns deleted=false when the share does not exist or the user is neither owner nor target.
func DeleteShare(id int64, userID string) (deleted bool, err error) {
	res := db.Where("id = ? AND (owner_id = ? OR target_user_id = ?)", id, userID, userID).Delete(&Share{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// UpdateShareFileSize updates the file_size for a share after an edit.
func UpdateShareFileSize(shareID string, newSize int64) error {
	return db.Model(&Share{}).Where("share_id = ?", shareID).Update("file_size", newSize).Error
}

// DeleteSharesByPath removes all shares for a file (e.g., when file is deleted).
func DeleteSharesByPath(ownerID, filePath string) error {
	return db.Where("owner_id = ? AND file_path = ?", ownerID, filePath).Delete(&Share{}).Error
}

// DeleteSharesByPrefix removes all shares under a path prefix (used for directory deletes).
func DeleteSharesByPrefix(ownerID, prefix string) error {
	return db.Where("owner_id = ? AND file_path LIKE ?", ownerID, prefix+"%").Delete(&Share{}).Error
}

// MoveSharesByPath updates share paths when a single file is renamed or moved.
func MoveSharesByPath(ownerID, oldPath, newPath string) error {
	return db.Model(&Share{}).
		Where("owner_id = ? AND file_path = ?", ownerID, oldPath).
		Updates(map[string]interface{}{
			"file_path": newPath,
			"file_name": filepath.Base(newPath),
		}).Error
}

// MoveSharesByPrefix updates share paths when a directory is renamed or moved.
func MoveSharesByPrefix(ownerID, oldPrefix, newPrefix string) error {
	return db.Model(&Share{}).
		Where("owner_id = ? AND file_path LIKE ?", ownerID, oldPrefix+"%").
		Updates(map[string]interface{}{
			"file_path": gorm.Expr("REPLACE(file_path, ?, ?)", oldPrefix, newPrefix),
		}).Error
}

// GetShareForUser returns a specific share of a file for a target user.
func GetShareForUser(filePath, targetUserID string) (*Share, error) {
	var s Share
	if err := db.Where("file_path = ? AND share_type = 'user' AND target_user_id = ?", filePath, targetUserID).
		First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}
