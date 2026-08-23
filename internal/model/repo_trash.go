package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrTrashStateConflict = errors.New("trash entry state conflict")

type gormTrashRepo struct{ db *gorm.DB }

func (r *gormTrashRepo) Create(entry *TrashEntry) error {
	if entry.DeletedAt.IsZero() {
		entry.DeletedAt = time.Now().UTC()
	}
	if entry.State == "" {
		entry.State = TrashStatePending
	}
	return r.db.Create(entry).Error
}

func (r *gormTrashRepo) Get(userID, id string) (*TrashEntry, error) {
	var entry TrashEntry
	if err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&entry).Error; err != nil {
		return nil, err
	}
	return &entry, nil
}

func (r *gormTrashRepo) List(userID string, limit, offset int) ([]TrashEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var entries []TrashEntry
	err := r.db.Where("user_id = ? AND state = ?", userID, TrashStateReady).
		Order("deleted_at DESC, id DESC").Limit(limit).Offset(offset).Find(&entries).Error
	return entries, err
}

func (r *gormTrashRepo) ListRecoverable() ([]TrashEntry, error) {
	var entries []TrashEntry
	err := r.db.Where("state <> ?", TrashStateReady).Order("updated_at ASC").Find(&entries).Error
	return entries, err
}

func (r *gormTrashRepo) Transition(userID, id, fromState, toState string, operationInode int64, operationPath, targetPath string) error {
	result := r.db.Model(&TrashEntry{}).
		Where("user_id = ? AND id = ? AND state = ?", userID, id, fromState).
		Updates(map[string]any{
			"state":           toState,
			"operation_inode": operationInode,
			"operation_path":  operationPath,
			"target_path":     targetPath,
			"updated_at":      time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTrashStateConflict
	}
	return nil
}

func (r *gormTrashRepo) MarkReady(userID, id, fromState string) error {
	return r.Transition(userID, id, fromState, TrashStateReady, 0, "", "")
}

func (r *gormTrashRepo) Delete(userID, id string) error {
	return r.db.Where("user_id = ? AND id = ?", userID, id).Delete(&TrashEntry{}).Error
}

func (r *gormTrashRepo) DeleteByUserID(userID string) error {
	return r.db.Where("user_id = ?", userID).Delete(&TrashEntry{}).Error
}
