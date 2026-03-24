package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"zephyr/internal/auth"
	"zephyr/internal/model"
	"zephyr/internal/service"
)

// ThumbnailParams holds parameters for a thumbnail generation job.
type ThumbnailParams struct {
	SourceKey    string `json:"source_key"`
	ThumbnailKey string `json:"thumbnail_key"`
	FileName     string `json:"file_name"`
	MediaType    string `json:"media_type"` // "image" or "video"
	TempDir      string `json:"temp_dir"`
}

// RunThumbnailJob generates a thumbnail for an image or video file.
// The thumbnail is stored unencrypted in OSS for direct browser access via presigned URL.
func (h *Handler) RunThumbnailJob(ctx context.Context, job *model.Job) error {
	var params ThumbnailParams
	if err := json.Unmarshal([]byte(job.Params), &params); err != nil {
		return fmt.Errorf("parse params: %w", err)
	}

	if err := os.MkdirAll(params.TempDir, 0755); err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(params.TempDir) }()

	// Check if thumbnail already exists in OSS (content-hash dedup)
	if _, err := h.Store.GetObjectInfo(params.ThumbnailKey); err == nil {
		// Thumbnail already exists — still need to probe for media info
		transcoder := service.NewTranscoder(h.Config.Transcode)
		inputExt := filepath.Ext(params.FileName)
		inputPath := filepath.Join(params.TempDir, "input"+inputExt)

		if dlErr := h.Store.DownloadToFile(params.SourceKey, inputPath); dlErr == nil {
			encKey, _ := auth.DeriveKey(h.Config.Server.EncryptionSecret, job.UserID)
			decPath := inputPath + ".dec"
			if decErr := auth.DecryptFile(encKey, inputPath, decPath); decErr == nil {
				_ = os.Remove(inputPath)
				_ = os.Rename(decPath, inputPath)
				if probe, probeErr := transcoder.Probe(ctx, inputPath); probeErr == nil && probe != nil {
					_ = model.UpdateFileThumbnail(job.UserID, params.SourceKey, params.ThumbnailKey, probe.Width, probe.Height, probe.Duration)
					return nil
				}
			}
		}
		// Fallback: update thumbnail key without media info
		_ = model.UpdateFileThumbnail(job.UserID, params.SourceKey, params.ThumbnailKey, 0, 0, 0)
		return nil
	}

	// Phase 1: Download and decrypt source file
	_ = model.UpdateJobProgress(job.JobID, 0.0, "downloading")
	inputExt := filepath.Ext(params.FileName)
	inputPath := filepath.Join(params.TempDir, "input"+inputExt)

	if err := h.Store.DownloadToFile(params.SourceKey, inputPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	encKey, err := auth.DeriveKey(h.Config.Server.EncryptionSecret, job.UserID)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}
	decPath := inputPath + ".dec"
	if err := auth.DecryptFile(encKey, inputPath, decPath); err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}
	_ = os.Remove(inputPath)
	if err := os.Rename(decPath, inputPath); err != nil {
		return fmt.Errorf("rename decrypted: %w", err)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 2: Generate thumbnail with ffmpeg
	_ = model.UpdateJobProgress(job.JobID, 0.3, "generating")
	thumbPath := filepath.Join(params.TempDir, "thumb.webp")

	var args []string
	switch params.MediaType {
	case "video":
		args = []string{
			"-i", inputPath,
			"-vf", "thumbnail,scale=480:-1",
			"-frames:v", "1",
			"-y", thumbPath,
		}
	case "image":
		args = []string{
			"-i", inputPath,
			"-vf", "scale=480:-1",
			"-y", thumbPath,
		}
	default:
		return fmt.Errorf("unsupported media type: %s", params.MediaType)
	}

	cmd := exec.CommandContext(ctx, h.Config.Transcode.FFmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, out)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 3: Probe media info
	_ = model.UpdateJobProgress(job.JobID, 0.6, "probing")
	transcoder := service.NewTranscoder(h.Config.Transcode)
	probe, _ := transcoder.Probe(ctx, inputPath)
	var width, height int
	var duration float64
	if probe != nil {
		width = probe.Width
		height = probe.Height
		duration = probe.Duration
	}

	// Phase 4: Upload thumbnail (unencrypted) to OSS
	_ = model.UpdateJobProgress(job.JobID, 0.8, "uploading")
	if err := h.Store.UploadFromFile(params.ThumbnailKey, thumbPath); err != nil {
		return fmt.Errorf("upload thumbnail: %w", err)
	}

	// Update file record with thumbnail key and media info
	_ = model.UpdateFileThumbnail(job.UserID, params.SourceKey, params.ThumbnailKey, width, height, duration)
	_ = model.UpdateJobResult(job.JobID, `{"ok":true}`)

	return nil
}
