package model

import (
	"time"

	"gorm.io/gorm"
)

// WorkspaceRepo defines workspace state data access operations.
type WorkspaceRepo interface {
	Save(userID, state string) error
	Get(userID string) (string, error)
	Delete(userID string) error
}

type gormWorkspaceRepo struct{ db *gorm.DB }

func (r *gormWorkspaceRepo) Save(userID, state string) error {
	ws := WorkspaceState{
		UserID:    userID,
		State:     state,
		UpdatedAt: time.Now(),
	}
	return r.db.Save(&ws).Error
}

func (r *gormWorkspaceRepo) Get(userID string) (string, error) {
	var ws WorkspaceState
	err := r.db.Where("user_id = ?", userID).First(&ws).Error
	if err != nil {
		return "", err
	}
	return ws.State, nil
}

func (r *gormWorkspaceRepo) Delete(userID string) error {
	return r.db.Where("user_id = ?", userID).Delete(&WorkspaceState{}).Error
}
