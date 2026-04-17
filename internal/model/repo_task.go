package model

import (
	"time"

	"gorm.io/gorm"
)

// TaskUpdateFunc is the callback invoked when a task's progress or status changes.
type TaskUpdateFunc func(userID, taskID, taskType, name, status, clientInstanceID string, progress float64, phase string)

// TaskRepo defines task data access operations.
type TaskRepo interface {
	Create(userID, taskID, taskType, name string) error
	Get(taskID string) (*Task, error)
	UpdateProgress(taskID string, progress float64, phase string) error
	UpdateStatus(taskID, status string) error
	ListRecent(userID string) ([]Task, error)
	DeleteCompleted(userID string) error
	Delete(taskID string) error
}

type gormTaskRepo struct {
	db           *gorm.DB
	onTaskUpdate TaskUpdateFunc
}

func (r *gormTaskRepo) Create(userID, taskID, taskType, name string) error {
	return r.db.Create(&Task{
		UserID: userID,
		TaskID: taskID,
		Type:   taskType,
		Status: "running",
		Name:   name,
	}).Error
}

func (r *gormTaskRepo) Get(taskID string) (*Task, error) {
	t := &Task{}
	if err := r.db.Where("task_id = ?", taskID).First(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

func (r *gormTaskRepo) UpdateProgress(taskID string, progress float64, phase string) error {
	err := r.db.Model(&Task{}).Where("task_id = ?", taskID).Updates(map[string]interface{}{
		"progress":   progress,
		"phase":      phase,
		"updated_at": time.Now(),
	}).Error
	if err == nil && r.onTaskUpdate != nil {
		if t, e := r.Get(taskID); e == nil {
			clientInstanceID := ""
			if t.Type == "upload" {
				var rec FileRecord
				if err := r.db.Where("task_id = ?", t.TaskID).First(&rec).Error; err == nil {
					clientInstanceID = rec.ClientInstanceID
				}
			}
			r.onTaskUpdate(t.UserID, t.TaskID, t.Type, t.Name, t.Status, clientInstanceID, progress, phase)
		}
	}
	return err
}

func (r *gormTaskRepo) UpdateStatus(taskID, status string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if status == "completed" {
		updates["progress"] = 1.0
	}
	err := r.db.Model(&Task{}).Where("task_id = ?", taskID).Updates(updates).Error
	if err == nil && r.onTaskUpdate != nil {
		if t, e := r.Get(taskID); e == nil {
			clientInstanceID := ""
			if t.Type == "upload" {
				var rec FileRecord
				if err := r.db.Where("task_id = ?", t.TaskID).First(&rec).Error; err == nil {
					clientInstanceID = rec.ClientInstanceID
				}
			}
			r.onTaskUpdate(t.UserID, t.TaskID, t.Type, t.Name, status, clientInstanceID, t.Progress, t.Phase)
		}
	}
	return err
}

func (r *gormTaskRepo) ListRecent(userID string) ([]Task, error) {
	var tasks []Task
	if err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").Limit(50).Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *gormTaskRepo) DeleteCompleted(userID string) error {
	return r.db.Where("user_id = ? AND status IN ?", userID, []string{"completed", "failed", "cancelled"}).
		Delete(&Task{}).Error
}

func (r *gormTaskRepo) Delete(taskID string) error {
	return r.db.Where("task_id = ?", taskID).Delete(&Task{}).Error
}
