package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"

	"zephyr/internal/auth"
	"zephyr/internal/middleware"
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

// ThumbnailResult holds the output of thumbnail generation.
type ThumbnailResult struct {
	ThumbPath string
	Width     int
	Height    int
	Duration  float64
}

// GenerateThumbnailFromPlaintext generates a thumbnail from an already-decrypted file.
// inputPath must be a plaintext media file. Returns the thumbnail file path and media info.
func (h *Handler) GenerateThumbnailFromPlaintext(ctx context.Context, inputPath, mediaType, tempDir string) (*ThumbnailResult, error) {
	thumbPath := filepath.Join(tempDir, "thumb.webp")

	var args []string
	switch mediaType {
	case "video":
		args = []string{"-i", inputPath, "-vf", "thumbnail,scale=480:-1", "-frames:v", "1", "-y", thumbPath}
	case "image":
		args = []string{"-i", inputPath, "-vf", "scale=480:-1", "-y", thumbPath}
	default:
		return nil, fmt.Errorf("unsupported media type: %s", mediaType)
	}

	cmd := exec.CommandContext(ctx, h.Config.Transcode.FFmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w\n%s", err, out)
	}

	transcoder := service.NewTranscoder(h.Config.Transcode)
	probe, _ := transcoder.Probe(ctx, inputPath)
	var width, height int
	var duration float64
	if probe != nil {
		width = probe.Width
		height = probe.Height
		duration = probe.Duration
	}

	return &ThumbnailResult{ThumbPath: thumbPath, Width: width, Height: height, Duration: duration}, nil
}

// RunThumbnailJob generates a thumbnail for an image or video file (manual trigger / legacy).
// The thumbnail is encrypted with a per-file DEK before being stored in OSS.
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

		if err := h.Store.DownloadToFile(params.SourceKey, inputPath); err != nil {
			return fmt.Errorf("download source for probe: %w", err)
		}
		kek, err := h.loadUserKEK(job.UserID)
		if err != nil {
			return fmt.Errorf("load user KEK for probe: %w", err)
		}
		srcRecord, err := model.GetFile(job.UserID, params.SourceKey)
		if err != nil {
			return fmt.Errorf("get file record for probe: %w", err)
		}
		srcWrapped, err := hex.DecodeString(srcRecord.WrappedDEK)
		if err != nil {
			return fmt.Errorf("decode wrapped DEK for probe: %w", err)
		}
		srcDEK, err := auth.UnwrapDEK(kek, srcWrapped)
		if err != nil {
			return fmt.Errorf("unwrap DEK for probe: %w", err)
		}
		decPath := inputPath + ".dec"
		if err := auth.DecryptFile(srcDEK, inputPath, decPath); err != nil {
			return fmt.Errorf("decrypt source for probe: %w", err)
		}
		_ = os.Remove(inputPath)
		if err := os.Rename(decPath, inputPath); err != nil {
			return fmt.Errorf("rename decrypted for probe: %w", err)
		}

		var width, height int
		var duration float64
		if probe, probeErr := transcoder.Probe(ctx, inputPath); probeErr == nil && probe != nil {
			width = probe.Width
			height = probe.Height
			duration = probe.Duration
		}
		// Thumbnail already in OSS (encrypted by a previous upload of the same content).
		// Reuse its wrapped DEK: since the key path is per-user and the thumbnail was
		// encrypted with this user's KEK, we can look up the wrapped DEK from any file
		// record that already references this thumbnail key.
		thumbWrappedDEK := ""
		if srcRecord.ThumbnailKey == params.ThumbnailKey && srcRecord.ThumbnailWrappedDEK != "" {
			thumbWrappedDEK = srcRecord.ThumbnailWrappedDEK
		}
		_ = model.UpdateFileThumbnail(job.UserID, params.SourceKey, params.ThumbnailKey, thumbWrappedDEK, width, height, duration)

		if h.Hub != nil {
			parent := parentDirOf(params.SourceKey)
			if parent != "" {
				username := params.SourceKey
				if idx := strings.Index(username, "/"); idx > 0 {
					username = username[:idx]
				}
				appPath := toAppPath(parent, username)
				h.Hub.PushDirChanged(parent, appPath, "refresh")
			}
		}
		return nil
	}

	// Phase 1: Download and decrypt source file
	_ = model.UpdateJobProgress(job.JobID, 0.0, "downloading")
	inputExt := filepath.Ext(params.FileName)
	inputPath := filepath.Join(params.TempDir, "input"+inputExt)

	if err := h.Store.DownloadToFile(params.SourceKey, inputPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	kek, err := h.loadUserKEK(job.UserID)
	if err != nil {
		return fmt.Errorf("load user KEK: %w", err)
	}
	srcRecord, err := model.GetFile(job.UserID, params.SourceKey)
	if err != nil {
		return fmt.Errorf("get file record: %w", err)
	}
	srcWrapped, err := hex.DecodeString(srcRecord.WrappedDEK)
	if err != nil {
		return fmt.Errorf("decode wrapped DEK: %w", err)
	}
	srcDEK, err := auth.UnwrapDEK(kek, srcWrapped)
	if err != nil {
		return fmt.Errorf("unwrap DEK: %w", err)
	}
	decPath := inputPath + ".dec"
	if err := auth.DecryptFile(srcDEK, inputPath, decPath); err != nil {
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

	// Phase 4: Encrypt and upload thumbnail to OSS
	_ = model.UpdateJobProgress(job.JobID, 0.8, "uploading")
	thumbWrappedHex, err := h.encryptAndUploadThumbnail(job.UserID, params.ThumbnailKey, thumbPath)
	if err != nil {
		return fmt.Errorf("encrypt+upload thumbnail: %w", err)
	}

	// Update file record with thumbnail key and media info
	_ = model.UpdateFileThumbnail(job.UserID, params.SourceKey, params.ThumbnailKey, thumbWrappedHex, width, height, duration)
	_ = model.UpdateJobResult(job.JobID, `{"ok":true}`)

	// Notify directory subscribers so the file list refreshes with the new thumbnail
	if h.Hub != nil {
		parent := parentDirOf(params.SourceKey)
		if parent != "" {
			username := params.SourceKey
			if idx := strings.Index(username, "/"); idx > 0 {
				username = username[:idx]
			}
			appPath := toAppPath(parent, username)
			h.Hub.PushDirChanged(parent, appPath, "refresh")
		}
	}

	return nil
}

// encryptAndUploadThumbnail encrypts a thumbnail file with a new DEK wrapped
// by the user's KEK, then uploads the ciphertext to OSS. Returns the hex-encoded
// wrapped DEK for storage in the file record.
func (h *Handler) encryptAndUploadThumbnail(userID, ossKey, localPath string) (string, error) {
	kek, err := h.loadUserKEK(userID)
	if err != nil {
		return "", fmt.Errorf("load KEK: %w", err)
	}
	thumbDEK, err := auth.GenerateDEK()
	if err != nil {
		return "", fmt.Errorf("generate DEK: %w", err)
	}
	encPath := localPath + ".enc"
	if err := auth.EncryptFile(thumbDEK, localPath, encPath); err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	defer func() { _ = os.Remove(encPath) }()

	if err := h.Store.UploadFromFile(ossKey, encPath); err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	wrapped, err := auth.WrapDEK(kek, thumbDEK)
	if err != nil {
		return "", fmt.Errorf("wrap DEK: %w", err)
	}
	return hex.EncodeToString(wrapped), nil
}

// handleThumbnail serves a decrypted thumbnail for the requesting user's file.
func (h *Handler) handleThumbnail(c *fiber.Ctx) error {
	p := c.Query("path", "")
	if p == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path_required"})
	}

	resolvedPath, err := middleware.ResolvePath(c, p)
	if err != nil {
		return err
	}

	session := c.Locals("session").(*model.Session)
	fileRecord, err := model.GetFile(session.UserID, resolvedPath)
	if err != nil || fileRecord.ThumbnailKey == "" || fileRecord.ThumbnailWrappedDEK == "" {
		return c.Status(404).JSON(fiber.Map{"error": "not_found"})
	}

	kek, err := h.getFileEncryptionKey(session)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	wrappedBytes, err := hex.DecodeString(fileRecord.ThumbnailWrappedDEK)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal_error"})
	}
	thumbDEK, err := auth.UnwrapDEK(kek, wrappedBytes)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "unwrap_failed"})
	}

	reader, err := h.Store.GetObjectContent(fileRecord.ThumbnailKey)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "read_failed"})
	}
	defer reader.Close()

	c.Set("Content-Type", "image/webp")
	c.Set("Cache-Control", "private, max-age=3600")
	return auth.DecryptStream(thumbDEK, reader, c)
}
