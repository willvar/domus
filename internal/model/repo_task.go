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
	CreateQueued(userID, taskID, taskType, name string, sourceInode int64, sourcePath, profile string) error
	Get(taskID string) (*Task, error)
	UpdateProgress(taskID string, progress float64, phase string) error
	UpdateStatus(taskID, status string) error
	ClaimNextTranscode(userID string) (*Task, error)
	RequeueStaleTranscodes() ([]Task, error)
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

func (r *gormTaskRepo) CreateQueued(userID, taskID, taskType, name string, sourceInode int64, sourcePath, profile string) error {
	return r.db.Create(&Task{
		UserID:      userID,
		TaskID:      taskID,
		Type:        taskType,
		Status:      "queued",
		Name:        name,
		SourceInode: sourceInode,
		SourcePath:  sourcePath,
		Profile:     profile,
	}).Error
}

// ClaimNextTranscode atomically claims the oldest queued transcode task of a
// user by flipping it to running. An empty userID claims across all users.
// FOR UPDATE SKIP LOCKED keeps multiple worker processes from claiming the
// same job.
func (r *gormTaskRepo) ClaimNextTranscode(userID string) (*Task, error) {
	scope := r.db
	if userID != "" {
		scope = scope.Where("user_id = ?", userID)
	}
	var claimed Task
	err := scope.Raw(`UPDATE tasks SET status = 'running', updated_at = now()
		WHERE id = (SELECT id FROM tasks WHERE type = 'transcode' AND status = 'queued'
			ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING *`).Scan(&claimed).Error
	if err != nil {
		return nil, err
	}
	if claimed.TaskID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &claimed, nil
}

// RequeueStaleTranscodes resets transcode tasks stuck in running/cancelling —
// the residue of a worker process that died mid-job — back to queued so a
// restarted worker claims them again. Returns the affected tasks so callers
// can clean up matching rendition rows.
func (r *gormTaskRepo) RequeueStaleTranscodes() ([]Task, error) {
	var tasks []Task
	err := r.db.Raw(`UPDATE tasks SET status = 'queued', progress = 0, updated_at = now()
		WHERE type = 'transcode' AND status IN ('running', 'cancelling')
		RETURNING *`).Scan(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
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
