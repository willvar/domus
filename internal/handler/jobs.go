package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/model"
)

// JobResponse is the API representation of a job.
type JobResponse struct {
	JobID     string  `json:"job_id"`
	Type      string  `json:"type"`
	Status    string  `json:"status"`
	Progress  float64 `json:"progress"`
	Phase     string  `json:"phase,omitempty"`
	CreatedAt string  `json:"created_at"`
}

func jobToResponse(j model.Job) JobResponse {
	return JobResponse{
		JobID:     j.JobID,
		Type:      j.Type,
		Status:    j.Status,
		Progress:  j.Progress,
		Phase:     j.Phase,
		CreatedAt: j.CreatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) handleListJobs(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	jobs, err := model.ListRecentJobs(session.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "list_jobs_failed"})
	}
	resp := make([]JobResponse, len(jobs))
	for i, j := range jobs {
		resp[i] = jobToResponse(j)
	}
	return c.JSON(resp)
}

func (h *Handler) handleClearJobs(c *fiber.Ctx) error {
	session := c.Locals("session").(*model.Session)
	if err := model.DeleteCompletedJobs(session.UserID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "clear_jobs_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleJobStatus(c *fiber.Ctx) error {
	jobID := c.Params("id")
	session := c.Locals("session").(*model.Session)

	job, err := model.GetJobByJobID(jobID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "job_not_found"})
	}
	if job.UserID != session.UserID && session.Role != "admin" {
		return c.Status(403).JSON(fiber.Map{"error": "access_denied"})
	}

	// SSE stream
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		for {
			j, err := model.GetJobByJobID(jobID)
			if err != nil {
				return
			}

			resp := fiber.Map{
				"job_id":   j.JobID,
				"type":     j.Type,
				"status":   j.Status,
				"progress": j.Progress,
				"phase":    j.Phase,
			}
			if j.Status == "failed" {
				resp["error"] = "job_failed"
			}
			data, _ := json.Marshal(resp)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			_ = w.Flush()

			// Terminal states
			if j.Status == "completed" || j.Status == "failed" || j.Status == "aborted" {
				return
			}

			time.Sleep(1 * time.Second)
		}
	})
	return nil
}

func (h *Handler) handleCancelJob(c *fiber.Ctx) error {
	jobID := c.Params("id")
	session := c.Locals("session").(*model.Session)

	job, err := model.GetJobByJobID(jobID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "job_not_found"})
	}
	if job.UserID != session.UserID && session.Role != "admin" {
		return c.Status(403).JSON(fiber.Map{"error": "access_denied"})
	}

	h.Dispatcher.Cancel(jobID)
	return c.JSON(fiber.Map{"ok": true})
}
