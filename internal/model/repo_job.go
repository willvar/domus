package model

import (
	"time"

	"gorm.io/gorm"
)

// JobRepo defines job data access operations.
type JobRepo interface {
	Create(userID, jobID, jobType, params string) (*Job, error)
	CreateDirect(job *Job) error
	GetByJobID(jobID string) (*Job, error)
	ListActive(userID string) ([]Job, error)
	ListRecent(userID string) ([]Job, error)
	DeleteCompleted(userID string) error
	UpdateStatus(jobID, status string) error
	UpdateProgress(jobID string, progress float64, phase string) error
	UpdateResult(jobID, result string) error
	UpdateError(jobID, errorMsg string) error
	ClaimPending(jobType string) (*Job, error)
	FindActiveByParam(jobType, paramSubstr string) (*Job, error)
	ResetRunning() error
	FindByTaskID(taskID string) ([]Job, error)
}

type gormJobRepo struct {
	db    *gorm.DB
	tasks *gormTaskRepo
}

func (r *gormJobRepo) cascadeToTask(jobID string) {
	j, err := r.GetByJobID(jobID)
	if err != nil || j.TaskID == "" {
		return
	}
	switch j.Status {
	case "completed":
		_ = r.tasks.UpdateStatus(j.TaskID, "completed")
	case "failed":
		_ = r.tasks.UpdateProgress(j.TaskID, j.Progress, j.Phase)
		_ = r.tasks.UpdateStatus(j.TaskID, "failed")
	case "aborted":
		_ = r.tasks.UpdateStatus(j.TaskID, "cancelled")
	default:
		_ = r.tasks.UpdateProgress(j.TaskID, j.Progress, j.Phase)
	}
}

func (r *gormJobRepo) Create(userID, jobID, jobType, params string) (*Job, error) {
	job := &Job{
		UserID: userID,
		JobID:  jobID,
		Type:   jobType,
		Status: "pending",
		Params: params,
	}
	if err := r.db.Create(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

func (r *gormJobRepo) CreateDirect(job *Job) error {
	return r.db.Create(job).Error
}

func (r *gormJobRepo) GetByJobID(jobID string) (*Job, error) {
	j := &Job{}
	if err := r.db.Where("job_id = ?", jobID).First(j).Error; err != nil {
		return nil, err
	}
	return j, nil
}

func (r *gormJobRepo) ListActive(userID string) ([]Job, error) {
	var jobs []Job
	if err := r.db.Where("user_id = ? AND status IN ?", userID, []string{"pending", "running", "paused"}).
		Order("created_at DESC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *gormJobRepo) ListRecent(userID string) ([]Job, error) {
	var jobs []Job
	if err := r.db.Where("user_id = ? AND type IN ?", userID, []string{"transcode"}).
		Order("created_at DESC").Limit(50).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *gormJobRepo) DeleteCompleted(userID string) error {
	return r.db.Where("user_id = ? AND status IN ?", userID, []string{"completed", "failed", "aborted"}).
		Delete(&Job{}).Error
}

func (r *gormJobRepo) UpdateStatus(jobID, status string) error {
	err := r.db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		r.cascadeToTask(jobID)
	}
	return err
}

func (r *gormJobRepo) UpdateProgress(jobID string, progress float64, phase string) error {
	err := r.db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"progress":   progress,
		"phase":      phase,
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		r.cascadeToTask(jobID)
	}
	return err
}

func (r *gormJobRepo) UpdateResult(jobID, result string) error {
	err := r.db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"result":     result,
		"status":     "completed",
		"progress":   1.0,
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		r.cascadeToTask(jobID)
	}
	return err
}

func (r *gormJobRepo) UpdateError(jobID, errorMsg string) error {
	err := r.db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"error_msg":  errorMsg,
		"status":     "failed",
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		r.cascadeToTask(jobID)
	}
	return err
}

func (r *gormJobRepo) ClaimPending(jobType string) (*Job, error) {
	j := &Job{}
	result := r.db.Raw(
		"UPDATE jobs SET status = 'running', updated_at = NOW() WHERE id = (SELECT id FROM jobs WHERE status = 'pending' AND type = ? ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING *",
		jobType,
	).Scan(j)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return j, nil
}

func (r *gormJobRepo) FindActiveByParam(jobType, paramSubstr string) (*Job, error) {
	j := &Job{}
	err := r.db.Where("type = ? AND status IN ? AND params LIKE ?", jobType, []string{"uploading", "pending", "running"}, "%"+paramSubstr+"%").First(j).Error
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (r *gormJobRepo) ResetRunning() error {
	return r.db.Model(&Job{}).Where("status = ?", "running").Updates(map[string]interface{}{
		"status":     "pending",
		"updated_at": time.Now(),
	}).Error
}

func (r *gormJobRepo) FindByTaskID(taskID string) ([]Job, error) {
	var jobs []Job
	if err := r.db.Where("task_id = ?", taskID).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}
