package model

import "time"

// WorkspaceState stores serialised workspace snapshots per user.
type WorkspaceState struct {
	UserID    string    `gorm:"primaryKey" json:"user_id"`
	State     string    `gorm:"type:text" json:"state"` // JSON blob
	UpdatedAt time.Time `json:"updated_at"`
}

// SaveWorkspaceState upserts the workspace snapshot for a user.
func SaveWorkspaceState(userID, state string) error {
	ws := WorkspaceState{
		UserID:    userID,
		State:     state,
		UpdatedAt: time.Now(),
	}
	return db.Save(&ws).Error
}

// GetWorkspaceState returns the saved snapshot or empty string if none exists.
func GetWorkspaceState(userID string) (string, error) {
	var ws WorkspaceState
	err := db.Where("user_id = ?", userID).First(&ws).Error
	if err != nil {
		return "", err
	}
	return ws.State, nil
}

// DeleteWorkspaceState removes the workspace snapshot for a user.
func DeleteWorkspaceState(userID string) error {
	return db.Where("user_id = ?", userID).Delete(&WorkspaceState{}).Error
}
