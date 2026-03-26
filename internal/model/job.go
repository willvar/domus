package model

import "time"

// cascadeToTask propagates a job's progress/status update to its linked Task (if any).
func cascadeToTask(jobID string) {
	j, err := GetJobByJobID(jobID)
	if err != nil || j.TaskID == "" {
		return
	}
	switch j.Status {
	case "completed":
		_ = UpdateTaskStatus(j.TaskID, "completed")
	case "failed":
		_ = UpdateTaskProgress(j.TaskID, j.Progress, j.Phase)
		_ = UpdateTaskStatus(j.TaskID, "failed")
	case "aborted":
		_ = UpdateTaskStatus(j.TaskID, "cancelled")
	default:
		_ = UpdateTaskProgress(j.TaskID, j.Progress, j.Phase)
	}
}

type Job struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID    string    `gorm:"not null;index" json:"-"`
	JobID     string    `gorm:"uniqueIndex;not null" json:"-"`
	TaskID    string    `gorm:"default:'';index" json:"-"` // links to user-facing Task (empty for internal jobs)
	Type      string    `gorm:"not null;index" json:"-"`
	Status    string    `gorm:"not null;default:pending;index" json:"-"`
	Progress  float64   `gorm:"not null;default:0" json:"-"`
	Phase     string    `gorm:"default:''" json:"-"`
	Params    string    `gorm:"type:text;default:'{}'" json:"-"`
	Result    string    `gorm:"type:text;default:'{}'" json:"-"`
	ErrorMsg  string    `gorm:"default:''" json:"-"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (Job) TableName() string { return "jobs" }

// Job operations

func CreateJob(userID string, jobID, jobType, params string) (*Job, error) {
	job := &Job{
		UserID: userID,
		JobID:  jobID,
		Type:   jobType,
		Status: "pending",
		Params: params,
	}
	if err := db.Create(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

// CreateJobDirect inserts a job record with all fields as provided (including custom status).
func CreateJobDirect(job *Job) error {
	return db.Create(job).Error
}

func GetJobByJobID(jobID string) (*Job, error) {
	j := &Job{}
	if err := db.Where("job_id = ?", jobID).First(j).Error; err != nil {
		return nil, err
	}
	return j, nil
}

func ListActiveJobs(userID string) ([]Job, error) {
	var jobs []Job
	if err := db.Where("user_id = ? AND status IN ?", userID, []string{"pending", "running", "paused"}).
		Order("created_at DESC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// ListActiveUploadJobs returns active upload jobs for a user.
func ListActiveUploadJobs(userID string) ([]Job, error) {
	var jobs []Job
	if err := db.Where("user_id = ? AND type = ? AND status IN ?", userID, "upload", []string{"uploading", "pending", "running"}).
		Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func ListRecentJobs(userID string) ([]Job, error) {
	var jobs []Job
	if err := db.Where("user_id = ? AND type IN ?", userID, []string{"transcode", "upload"}).
		Order("created_at DESC").Limit(50).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func DeleteCompletedJobs(userID string) error {
	return db.Where("user_id = ? AND status IN ?", userID, []string{"completed", "failed", "aborted"}).
		Delete(&Job{}).Error
}

func UpdateJobStatus(jobID, status string) error {
	err := db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		cascadeToTask(jobID)
	}
	return err
}

func UpdateJobProgress(jobID string, progress float64, phase string) error {
	err := db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"progress":   progress,
		"phase":      phase,
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		cascadeToTask(jobID)
	}
	return err
}

func UpdateJobResult(jobID, result string) error {
	err := db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"result":     result,
		"status":     "completed",
		"progress":   1.0,
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		cascadeToTask(jobID)
	}
	return err
}

func UpdateJobError(jobID, errorMsg string) error {
	err := db.Model(&Job{}).Where("job_id = ?", jobID).Updates(map[string]interface{}{
		"error_msg":  errorMsg,
		"status":     "failed",
		"updated_at": time.Now(),
	}).Error
	if err == nil {
		cascadeToTask(jobID)
	}
	return err
}

func ClaimPendingJob(jobType string) (*Job, error) {
	j := &Job{}
	result := db.Raw(
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

// FindActiveJobByParam finds an active job whose params contain the given substring.
func FindActiveJobByParam(jobType, paramSubstr string) (*Job, error) {
	j := &Job{}
	err := db.Where("type = ? AND status IN ? AND params LIKE ?", jobType, []string{"uploading", "pending", "running"}, "%"+paramSubstr+"%").First(j).Error
	if err != nil {
		return nil, err
	}
	return j, nil
}

func ResetRunningJobs() error {
	return db.Model(&Job{}).Where("status = ?", "running").Updates(map[string]interface{}{
		"status":     "pending",
		"updated_at": time.Now(),
	}).Error
}
