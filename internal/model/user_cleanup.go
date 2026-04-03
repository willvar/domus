package model

import "gorm.io/gorm"

// DeleteUserAndRelatedData removes a user and all associated records that can
// become orphaned after account deletion.
func DeleteUserAndRelatedData(userID string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? OR target_user_id = ?", userID, userID).Delete(&Share{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&FileRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&TrashItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&Job{}).Error; err != nil {
			return err
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
