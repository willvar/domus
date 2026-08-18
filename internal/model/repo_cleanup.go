package model

import "gorm.io/gorm"

// UserCleanupRepo defines user cascade-delete operations.
type UserCleanupRepo interface {
	DeleteUserAndRelatedData(userID string) error
}

type gormUserCleanupRepo struct{ db *gorm.DB }

func (r *gormUserCleanupRepo) DeleteUserAndRelatedData(userID string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? OR target_user_id = ?", userID, userID).Delete(&Share{}).Error; err != nil {
			return err
		}
		for _, table := range []string{"domus_file_uploads", "domus_file_metadata"} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Exec("DELETE FROM "+table+" WHERE user_id = ?", userID).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&Task{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&WorkspaceState{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&DBSession{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", userID).Delete(&User{}).Error
	})
}
