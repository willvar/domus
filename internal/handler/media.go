package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"domus/internal/middleware"
	"domus/internal/model"
	workspaceRuntime "domus/internal/workspace"
)

type transcodeProfile struct {
	name        string
	extension   string
	contentType string
	accept      func(string) bool
}

var transcodeProfiles = map[string]transcodeProfile{
	"video-720p": {
		name: "video-720p", extension: ".720p.mp4", contentType: "video/mp4",
		accept: func(contentType string) bool { return strings.HasPrefix(contentType, "video/") },
	},
	"audio-mp3": {
		name: "audio-mp3", extension: ".mp3", contentType: "audio/mpeg",
		accept: func(contentType string) bool {
			return strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/")
		},
	},
}

func (h *Handler) handleTranscode(c *fiber.Ctx) error {
	var body struct {
		Path    string `json:"path"`
		Profile string `json:"profile"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Path) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_request"})
	}
	profile, ok := transcodeProfiles[strings.TrimSpace(body.Profile)]
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported_transcode_profile"})
	}
	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}
	session := c.Locals("session").(*model.Session)
	record, err := h.Repos.Files.Get(session.UserID, resolvedPath)
	if err != nil || record.IsDir || record.Status != "ready" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not_found"})
	}
	contentType := record.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(record.Name))
	}
	if !profile.accept(contentType) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported_media_type"})
	}

	taskID := uuid.NewString()
	jobContext, cancel, ok := h.reserveMediaJob(session.UserID, taskID)
	if !ok {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "media_job_capacity"})
	}
	baseName := strings.TrimSuffix(record.Name, path.Ext(record.Name))
	// The generated UUID suffix makes the default output collision-free while
	// retaining a human-readable profile suffix such as .720p.mp4.
	outputName := fmt.Sprintf("%s-%s%s", baseName, taskID[:8], profile.extension)
	outputLogicalPath := parentDirOf(record.Path) + outputName
	outputAppPath := toAppPath(outputLogicalPath, session.Username)
	if _, err := h.Repos.Files.Get(session.UserID, outputLogicalPath); err == nil {
		cancel()
		h.releaseMediaJob(session.UserID, taskID)
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "output_exists"})
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		cancel()
		h.releaseMediaJob(session.UserID, taskID)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "transcode_prepare_failed"})
	}
	if err := h.Repos.Tasks.Create(session.UserID, taskID, "transcode", outputName); err != nil {
		cancel()
		h.releaseMediaJob(session.UserID, taskID)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "create_task_failed"})
	}
	identity := workspaceRuntime.Identity{UserID: session.UserID, Username: session.Username}
	go h.runTranscodeJob(jobContext, cancel, taskID, identity, *record, outputLogicalPath, profile)
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"task_id": taskID, "output_path": outputAppPath, "profile": profile.name,
	})
}

func (h *Handler) enqueueServerPreview(identity workspaceRuntime.Identity, record model.FileRecord) {
	if record.IsDir || record.Status != "ready" || record.ThumbnailKey != "" ||
		strings.Contains(record.Path, "/.user/") || !previewableMedia(record) {
		return
	}
	taskID := uuid.NewString()
	jobContext, cancel, ok := h.reserveMediaJob(identity.UserID, taskID)
	if !ok {
		return
	}
	if err := h.Repos.Tasks.Create(identity.UserID, taskID, "preview", record.Name); err != nil {
		cancel()
		h.releaseMediaJob(identity.UserID, taskID)
		return
	}
	go h.runPreviewJob(jobContext, cancel, taskID, identity, record)
}

func previewableMedia(record model.FileRecord) bool {
	contentType := record.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(record.Name))
	}
	return strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") || contentType == "application/pdf"
}

func (h *Handler) mediaJobLimitPerUser() int {
	maxSessions := 0
	if h.Config != nil {
		maxSessions = h.Config.Workspace.MaxSessionsPerUser
	}
	if maxSessions <= 1 {
		return 0
	}
	return min(maxSessions-1, 2)
}

func (h *Handler) reserveMediaJob(userID, taskID string) (context.Context, context.CancelFunc, bool) {
	h.mediaMu.Lock()
	defer h.mediaMu.Unlock()
	if h.mediaClosing {
		return nil, nil, false
	}
	if h.mediaJobs == nil {
		h.mediaJobs = make(map[string]context.CancelFunc)
		h.mediaJobUsers = make(map[string]string)
		h.mediaJobsByUser = make(map[string]int)
		h.mediaIdleByUser = make(map[string]chan struct{})
		h.mediaBlocked = make(map[string]bool)
	}
	if h.mediaBlocked[userID] {
		return nil, nil, false
	}
	limit := h.mediaJobLimitPerUser()
	if limit == 0 || h.mediaJobsByUser[userID] >= limit {
		return nil, nil, false
	}
	timeout := 15 * time.Minute
	if h.Config != nil && h.Config.Workspace.ExecTimeoutSeconds > 0 {
		timeout = time.Duration(h.Config.Workspace.ExecTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	h.mediaJobs[taskID] = cancel
	h.mediaJobUsers[taskID] = userID
	if h.mediaJobsByUser[userID] == 0 {
		h.mediaIdleByUser[userID] = make(chan struct{})
	}
	h.mediaJobsByUser[userID]++
	h.mediaWG.Add(1)
	return ctx, cancel, true
}

func (h *Handler) releaseMediaJob(userID, taskID string) {
	h.mediaMu.Lock()
	delete(h.mediaJobs, taskID)
	delete(h.mediaJobUsers, taskID)
	if h.mediaJobsByUser[userID] <= 1 {
		delete(h.mediaJobsByUser, userID)
		if idle := h.mediaIdleByUser[userID]; idle != nil {
			close(idle)
			delete(h.mediaIdleByUser, userID)
		}
	} else {
		h.mediaJobsByUser[userID]--
	}
	h.mediaMu.Unlock()
	h.mediaWG.Done()
}

// drainMediaJobsForUser blocks new media work for a user, cancels every
// active job, and waits through its final cleanup. Account deletion must do
// this before removing the workspace, otherwise a cancelled job could race
// teardown and recreate the user's container or DOFS mount.
func (h *Handler) drainMediaJobsForUser(ctx context.Context, userID string) error {
	h.mediaMu.Lock()
	if h.mediaBlocked == nil {
		h.mediaBlocked = make(map[string]bool)
	}
	h.mediaBlocked[userID] = true
	idle := h.mediaIdleByUser[userID]
	cancels := make([]context.CancelFunc, 0, h.mediaJobsByUser[userID])
	for taskID, cancel := range h.mediaJobs {
		if h.mediaJobUsers[taskID] == userID {
			cancels = append(cancels, cancel)
		}
	}
	h.mediaMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if idle == nil {
		return nil
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) allowMediaJobsForUser(userID string) {
	h.mediaMu.Lock()
	delete(h.mediaBlocked, userID)
	h.mediaMu.Unlock()
}

func (h *Handler) cancelMediaJob(taskID string) bool {
	h.mediaMu.Lock()
	cancel, ok := h.mediaJobs[taskID]
	h.mediaMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// ShutdownMediaJobs prevents new preview/transcode jobs, cancels all active
// jobs, and waits until their DOFS cleanup and task-state updates finish.
func (h *Handler) ShutdownMediaJobs(ctx context.Context) error {
	h.mediaMu.Lock()
	h.mediaClosing = true
	cancels := make([]context.CancelFunc, 0, len(h.mediaJobs))
	for _, cancel := range h.mediaJobs {
		cancels = append(cancels, cancel)
	}
	h.mediaMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		h.mediaWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func logicalWorkspacePath(identity workspaceRuntime.Identity, logicalPath string) (string, error) {
	prefix := identity.Username + "/"
	if !strings.HasPrefix(logicalPath, prefix) {
		return "", errors.New("logical media path escaped the user namespace")
	}
	relative := strings.TrimPrefix(logicalPath, prefix)
	clean := path.Clean("/" + relative)
	if clean == "/" || strings.Contains(clean, "\x00") {
		return "", errors.New("invalid media path")
	}
	return "/workspace" + clean, nil
}

func (h *Handler) runPreviewJob(ctx context.Context, cancel context.CancelFunc, taskID string, identity workspaceRuntime.Identity, source model.FileRecord) {
	defer cancel()
	defer h.releaseMediaJob(identity.UserID, taskID)
	_ = h.Repos.Tasks.UpdateProgress(taskID, 0.05, "generating")
	sourcePath, err := logicalWorkspacePath(identity, source.Path)
	if err != nil {
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	outputLogicalPath := parentDirOf(source.Path) + ".user/derived/" + strconv.FormatInt(source.ID, 10) + "-g" + strconv.FormatInt(source.Generation, 10) + ".preview.jpg"
	outputPath, err := logicalWorkspacePath(identity, outputLogicalPath)
	if err != nil {
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	err = h.execMediaCommand(ctx, identity, []string{"mkdir", "-p", path.Dir(outputPath)})
	if err == nil {
		err = h.execMediaCommand(ctx, identity, []string{"/usr/local/bin/domus-preview", sourcePath, outputPath})
	}
	if err != nil {
		h.cleanupDerivedOutput(identity, outputPath)
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	_ = h.Repos.Tasks.UpdateProgress(taskID, 0.85, "thumbnail")
	thumbnail, err := h.Repos.Files.Get(identity.UserID, outputLogicalPath)
	if err != nil || thumbnail.Status != "ready" || thumbnail.WrappedDEK == "" {
		if err == nil {
			err = errors.New("DOFS did not publish the generated thumbnail")
		}
		h.cleanupDerivedOutput(identity, outputPath)
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	width, height, duration := h.probeMedia(ctx, identity, sourcePath)
	updated, err := h.Repos.Files.UpdateThumbnailIfGeneration(
		identity.UserID, source.Path, source.ID, source.Generation,
		thumbnail.StorageKey(), thumbnail.WrappedDEK, width, height, duration,
	)
	if err != nil {
		h.cleanupDerivedOutput(identity, outputPath)
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	if !updated {
		h.cleanupDerivedOutput(identity, outputPath)
		h.finishMediaJob(taskID, ctx, errors.New("source changed while generating preview"))
		return
	}
	if h.Hub != nil {
		h.notifyParentDir(identity.Username, source.Path)
	}
	h.finishMediaJob(taskID, ctx, nil)
}

func (h *Handler) runTranscodeJob(ctx context.Context, cancel context.CancelFunc, taskID string, identity workspaceRuntime.Identity, source model.FileRecord, outputLogicalPath string, profile transcodeProfile) {
	defer cancel()
	defer h.releaseMediaJob(identity.UserID, taskID)
	_ = h.Repos.Tasks.UpdateProgress(taskID, 0.05, "transcoding")
	sourcePath, err := logicalWorkspacePath(identity, source.Path)
	if err != nil {
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	outputPath, err := logicalWorkspacePath(identity, outputLogicalPath)
	temporaryLogicalPath := parentDirOf(source.Path) + ".user/derived/transcode-" + taskID + ".partial" + profile.extension
	temporaryPath, temporaryErr := logicalWorkspacePath(identity, temporaryLogicalPath)
	if err == nil && temporaryErr != nil {
		err = temporaryErr
	}
	if err == nil {
		err = h.execMediaCommand(ctx, identity, []string{"mkdir", "-p", path.Dir(temporaryPath)})
	}
	if err == nil {
		err = h.execMediaCommand(ctx, identity, []string{"/usr/local/bin/domus-transcode", profile.name, sourcePath, temporaryPath})
	}
	if err == nil {
		// Publish with a no-clobber rename so a file created by the user while
		// transcoding is never overwritten. A remaining source proves mv
		// declined the collision even on implementations where -n exits zero.
		err = h.execMediaCommand(ctx, identity, []string{"mv", "--no-clobber", "--", temporaryPath, outputPath})
	}
	if err == nil {
		err = h.execMediaCommand(ctx, identity, []string{"test", "!", "-e", temporaryPath})
	}
	if err != nil {
		h.cleanupDerivedOutput(identity, temporaryPath)
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	_ = h.Repos.Tasks.UpdateProgress(taskID, 0.9, "finalizing")
	output, err := h.Repos.Files.Get(identity.UserID, outputLogicalPath)
	if err != nil || output.Status != "ready" {
		if err == nil {
			err = errors.New("DOFS did not publish the transcoded file")
		}
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	if err := h.Repos.Files.UpdateContentType(identity.UserID, outputLogicalPath, profile.contentType); err != nil {
		h.finishMediaJob(taskID, ctx, err)
		return
	}
	if h.Hub != nil {
		h.notifyParentDir(identity.Username, outputLogicalPath)
	}
	h.finishMediaJob(taskID, ctx, nil)
}

func (h *Handler) execMediaCommand(ctx context.Context, identity workspaceRuntime.Identity, command []string) error {
	result, err := h.Workspace.Exec(ctx, workspaceRuntime.ExecRequest{
		Identity: identity, Command: command, WorkingDir: "/workspace",
	})
	if err != nil {
		return err
	}
	if result.Truncated {
		return workspaceRuntime.ErrOutputLimit
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(string(result.Stderr))
		if len(message) > 2048 {
			message = message[:2048]
		}
		if message == "" {
			message = fmt.Sprintf("media command exited with status %d", result.ExitCode)
		}
		return errors.New(message)
	}
	return nil
}

func (h *Handler) probeMedia(ctx context.Context, identity workspaceRuntime.Identity, sourcePath string) (int, int, float64) {
	result, err := h.Workspace.Exec(ctx, workspaceRuntime.ExecRequest{
		Identity: identity,
		Command: []string{
			"ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height",
			"-show_entries", "format=duration", "-of", "json", sourcePath,
		},
		WorkingDir: "/workspace",
	})
	if err != nil || result.ExitCode != 0 {
		return 0, 0, 0
	}
	var probe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(result.Stdout, &probe); err != nil {
		return 0, 0, 0
	}
	width, height := 0, 0
	if len(probe.Streams) > 0 {
		width, height = probe.Streams[0].Width, probe.Streams[0].Height
	}
	duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	return width, height, duration
}

func (h *Handler) cleanupDerivedOutput(identity workspaceRuntime.Identity, outputPath string) {
	if outputPath == "" || !strings.HasPrefix(outputPath, "/workspace/") {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.execMediaCommand(cleanupCtx, identity, []string{"rm", "-f", outputPath}); err != nil {
		log.Printf("[media] cleanup %s for user %s failed: %v", outputPath, identity.UserID, err)
	}
}

func (h *Handler) finishMediaJob(taskID string, ctx context.Context, err error) {
	status := "completed"
	if err != nil {
		status = "failed"
		if ctx.Err() != nil {
			status = "cancelled"
		}
		log.Printf("[media] task %s %s: %v", taskID, status, err)
	}
	_ = h.Repos.Tasks.UpdateStatus(taskID, status)
}
