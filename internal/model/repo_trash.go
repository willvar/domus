package model

import "gorm.io/gorm"

// TrashRepo defines trash record data access operations.
type TrashRepo interface {
	Create(userID, originalPath, trashKey string, size int64, isDir bool) error
	List(userID string) ([]TrashItem, error)
	Get(id int64, userID string) (*TrashItem, error)
	Delete(id int64) error
	Clear(userID string) ([]TrashItem, error)
}

type gormTrashRepo struct{ db *gorm.DB }

func (r *gormTrashRepo) Create(userID, originalPath, trashKey string, size int64, isDir bool) error {
	return r.db.Create(&TrashItem{
		UserID:       userID,
		OriginalPath: originalPath,
		TrashKey:     trashKey,
		Size:         size,
		IsDir:        isDir,
	}).Error
}

func (r *gormTrashRepo) List(userID string) ([]TrashItem, error) {
	var items []TrashItem
	if err := r.db.Where("user_id = ?", userID).Order("deleted_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *gormTrashRepo) Get(id int64, userID string) (*TrashItem, error) {
	t := &TrashItem{}
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

func (r *gormTrashRepo) Delete(id int64) error {
	return r.db.Delete(&TrashItem{}, id).Error
}

func (r *gormTrashRepo) Clear(userID string) ([]TrashItem, error) {
	var items []TrashItem
	if err := r.db.Where("user_id = ?", userID).Order("deleted_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	if err := r.db.Where("user_id = ?", userID).Delete(&TrashItem{}).Error; err != nil {
		return nil, err
	}
	return items, nil
}
