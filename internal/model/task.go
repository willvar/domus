package model

import "time"

// Task is a user-facing task visible in the task panel.
// It tracks progress across the full lifecycle (e.g., upload → server processing).
// Jobs are internal dispatcher work items that may be linked to a Task via TaskID.
type Task struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID    string    `gorm:"not null;index" json:"-"`
	TaskID    string    `gorm:"uniqueIndex;not null" json:"task_id"`
	Type      string    `gorm:"not null" json:"type"`                     // "upload", "transcode"
	Status    string    `gorm:"not null;default:'running'" json:"status"` // running, completed, failed, cancelled
	Progress  float64   `gorm:"not null;default:0" json:"progress"`
	Phase     string    `gorm:"default:''" json:"phase"`
	Name      string    `gorm:"default:''" json:"name"` // display name (filename, etc.)
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Task) TableName() string { return "tasks" }

// OnTaskUpdate is called when a task's progress or status changes.
// Set by the server at startup to push updates to WebSocket clients.
var OnTaskUpdate func(userID, taskID, taskType, name, status string, progress float64, phase string)

func CreateTask(userID, taskID, taskType, name string) error {
	return db.Create(&Task{
		UserID: userID,
		TaskID: taskID,
		Type:   taskType,
		Status: "running",
		Name:   name,
	}).Error
}

func GetTask(taskID string) (*Task, error) {
	t := &Task{}
	if err := db.Where("task_id = ?", taskID).First(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

func UpdateTaskProgress(taskID string, progress float64, phase string) error {
	err := db.Model(&Task{}).Where("task_id = ?", taskID).Updates(map[string]interface{}{
		"progress":   progress,
		"phase":      phase,
		"updated_at": time.Now(),
	}).Error
	if err == nil && OnTaskUpdate != nil {
		if t, e := GetTask(taskID); e == nil {
			OnTaskUpdate(t.UserID, t.TaskID, t.Type, t.Name, t.Status, progress, phase)
		}
	}
	return err
}

func UpdateTaskStatus(taskID, status string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if status == "completed" {
		updates["progress"] = 1.0
	}
	err := db.Model(&Task{}).Where("task_id = ?", taskID).Updates(updates).Error
	if err == nil && OnTaskUpdate != nil {
		if t, e := GetTask(taskID); e == nil {
			OnTaskUpdate(t.UserID, t.TaskID, t.Type, t.Name, status, t.Progress, t.Phase)
		}
	}
	return err
}

func ListRecentTasks(userID string) ([]Task, error) {
	var tasks []Task
	if err := db.Where("user_id = ?", userID).
		Order("created_at DESC").Limit(50).Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func DeleteCompletedTasks(userID string) error {
	return db.Where("user_id = ? AND status IN ?", userID, []string{"completed", "failed", "cancelled"}).
		Delete(&Task{}).Error
}

func DeleteTask(taskID string) error {
	return db.Where("task_id = ?", taskID).Delete(&Task{}).Error
}
