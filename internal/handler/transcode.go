package handler

import (
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/willvar/dofs"

	"domus/internal/auth"
	"domus/internal/fileview"
	"domus/internal/httpx"
	"domus/internal/middleware"
	"domus/internal/worker"
)

// handleTranscodeRequest queues a server-side transcode of a video file into
// one fixed playback-quality profile. The rendition row doubles as a
// reservation: an existing queued/running/ready rendition for the same source
// generation refuses duplicates.
func (h *Handler) handleTranscodeRequest(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	var body struct {
		Path    string `json:"path"`
		Profile string `json:"profile"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_body"})
	}
	resolvedPath, err := middleware.ResolvePath(r, body.Path)
	if err != nil {
		return err
	}
	profile, ok := worker.FindTranscodeProfile(strings.TrimSpace(body.Profile))
	if !ok {
		return httpx.JSON(w, 400, map[string]any{"error": "invalid_profile"})
	}
	record, err := h.FileSystem.Get(session.UserID, resolvedPath)
	if err != nil {
		return httpx.JSON(w, 404, map[string]any{"error": "file_not_found"})
	}
	if record.IsDir || record.Status != "ready" {
		return httpx.JSON(w, 409, map[string]any{"error": "file_not_ready"})
	}
	if !strings.HasPrefix(record.ContentType, "video/") {
		return httpx.JSON(w, 400, map[string]any{"error": "not_a_video"})
	}
	renditions, err := h.FileSystem.ListRenditions(session.UserID, uint64(record.ID))
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "list_renditions_failed"})
	}
	for _, rendition := range renditions {
		if rendition.Profile != profile.ID {
			continue
		}
		switch rendition.Status {
		case "ready":
			return httpx.JSON(w, 409, map[string]any{"error": "rendition_ready"})
		case "queued", "running", "cancelling":
			return httpx.JSON(w, 409, map[string]any{"error": "rendition_in_progress"})
		}
	}
	if _, err := h.FileSystem.CreateRendition(session.UserID, record, profile.ID); err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "create_rendition_failed"})
	}
	taskID := uuid.NewString()
	if err := h.Repos.Tasks.CreateQueued(session.UserID, taskID, "transcode", record.Name,
		record.ID, record.Path, profile.ID); err != nil {
		_ = h.FileSystem.DeleteRendition(session.UserID, uint64(record.ID), profile.ID)
		return httpx.JSON(w, 500, map[string]any{"error": "create_task_failed"})
	}
	if err := h.FileSystem.SetRenditionTask(session.UserID, uint64(record.ID), profile.ID, taskID); err != nil {
		_ = h.FileSystem.DeleteRendition(session.UserID, uint64(record.ID), profile.ID)
		_ = h.Repos.Tasks.Delete(taskID)
		return httpx.JSON(w, 500, map[string]any{"error": "create_task_failed"})
	}
	h.notifyParentDir(session.UserID, record.Path)
	return httpx.JSON(w, http.StatusOK, map[string]any{"task_id": taskID, "profile": profile.ID, "status": "queued"})
}

// handleListRenditions returns the quality menu projection of one file: each
// rendition with its status and — for playable ones — the resolved artifact
// manifest (presigned URLs + plaintext DEKs). Progress of a running rendition
// mirrors its task row.
func (h *Handler) handleListRenditions(w http.ResponseWriter, r *http.Request) error {
	session := middleware.Session(r)
	resolvedPath, err := middleware.ResolvePath(r, httpx.Query(r, "path"))
	if err != nil {
		return err
	}
	record, err := h.FileSystem.Get(session.UserID, resolvedPath)
	if err != nil {
		return httpx.JSON(w, http.StatusOK, map[string]any{"renditions": []map[string]any{}})
	}
	if record.IsDir || !strings.HasPrefix(record.ContentType, "video/") {
		return httpx.JSON(w, http.StatusOK, map[string]any{"renditions": []map[string]any{}})
	}
	renditions, err := h.FileSystem.ListRenditions(session.UserID, uint64(record.ID))
	if err != nil {
		return httpx.JSON(w, 500, map[string]any{"error": "list_renditions_failed"})
	}
	kek, _ := h.getFileEncryptionKey(session)
	out := make([]map[string]any, 0, len(renditions))
	for _, rendition := range renditions {
		item := map[string]any{
			"profile": rendition.Profile,
			"status":  rendition.Status,
			"task_id": rendition.TaskID,
			"codecs":  rendition.Codecs,
		}
		if rendition.MediaWidth > 0 {
			item["width"] = rendition.MediaWidth
			item["height"] = rendition.MediaHeight
		}
		if rendition.Error != "" {
			item["error"] = rendition.Error
		}
		if task, taskErr := h.Repos.Tasks.Get(rendition.TaskID); taskErr == nil {
			item["progress"] = task.Progress
		}
		if kek != nil && (rendition.Status == "ready" || rendition.Status == "running") {
			total := 0.0
			segments := make([]map[string]any, 0, len(rendition.Segments))
			for _, segment := range rendition.Segments {
				total += segment.Duration
				segments = append(segments, h.renditionArtifactPayload(segment, kek))
			}
			if rendition.Init != nil {
				item["init"] = h.renditionArtifactPayload(*rendition.Init, kek)
			}
			item["segments"] = segments
			item["duration"] = total
		}
		out = append(out, item)
	}
	// Source geometry drives the menu: profiles at or above the source height
	// offer no benefit (transcodes never upscale), so they are not offered.
	return httpx.JSON(w, http.StatusOK, map[string]any{
		"source_width":  record.MediaWidth,
		"source_height": record.MediaHeight,
		"renditions":    out,
	})
}

// renditionArtifactPayload resolves one derived artifact into the browser
// playback contract: presigned URL + plaintext DEK.
func (h *Handler) renditionArtifactPayload(artifact fileview.RenditionArtifact, kek []byte) map[string]any {
	payload := map[string]any{"size": artifact.Size}
	if artifact.Duration > 0 {
		payload["duration"] = artifact.Duration
	}
	presignedURL, err := h.Store.GeneratePresignedURL(artifact.ObjectKey, 4*time.Hour)
	if err == nil {
		payload["url"] = presignedURL
	}
	wrappedBytes, err := hex.DecodeString(artifact.WrappedDEK)
	if err != nil {
		return payload
	}
	dek, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return payload
	}
	defer dofs.Clear(dek)
	payload["dek"] = hex.EncodeToString(dek)
	return payload
}
