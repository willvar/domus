package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"zephyr/config"
	"zephyr/internal/auth"
	"zephyr/internal/middleware"
	"zephyr/internal/model"
	"zephyr/internal/service"
)

// TranscodeParams holds parameters for a transcode job.
type TranscodeParams struct {
	SourceKey    string `json:"source_key"`
	TargetKey    string `json:"target_key"`
	MediaType    string `json:"media_type"`
	Preset       string `json:"preset"`
	OutputFormat string `json:"output_format"`
	Replace      bool   `json:"replace"`
	OriginalName string `json:"original_name"`
	OutputName   string `json:"output_name"`
	FileSize     int64  `json:"file_size"`
	TempDir      string `json:"temp_dir"`
}

// TranscodeResult holds the result of a transcode job.
type TranscodeResult struct {
	OutputSize int64 `json:"output_size"`
	DurationMs int64 `json:"duration_ms"`
}

func (h *Handler) handleTranscodeStart(c *fiber.Ctx) error {
	var body struct {
		Path         string `json:"path"`
		Preset       string `json:"preset"`
		OutputFormat string `json:"output_format"`
		Replace      bool   `json:"replace"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}

	session := c.Locals("session").(*model.Session)

	resolvedPath, err := middleware.ResolvePath(c, body.Path)
	if err != nil {
		return err
	}

	// Get file info
	info, err := h.Store.GetObjectInfo(resolvedPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file_not_found"})
	}

	originalName := filepath.Base(resolvedPath)
	mediaType := service.DetectMediaType(originalName)
	if mediaType == "" {
		return c.Status(400).JSON(fiber.Map{"error": "unsupported_media_type"})
	}

	if body.Preset == "" {
		body.Preset = "medium"
	}
	if body.OutputFormat == "" {
		switch mediaType {
		case "video":
			body.OutputFormat = "mp4"
		case "audio":
			body.OutputFormat = "aac"
		case "image":
			body.OutputFormat = "webp"
		}
	}

	// Build output name and key
	ext := service.OutputExtension(body.OutputFormat)
	nameWithoutExt := strings.TrimSuffix(originalName, filepath.Ext(originalName))
	outputName := nameWithoutExt + ext

	var targetKey string
	if body.Replace {
		targetKey = resolvedPath
	} else {
		dir := filepath.Dir(resolvedPath)
		targetKey = dir + "/" + outputName
	}

	jobID := uuid.New().String()
	tempDir := filepath.Join(config.TempDir, "transcode", jobID)

	params := TranscodeParams{
		SourceKey:    resolvedPath,
		TargetKey:    targetKey,
		MediaType:    mediaType,
		Preset:       body.Preset,
		OutputFormat: body.OutputFormat,
		Replace:      body.Replace,
		OriginalName: originalName,
		OutputName:   outputName,
		FileSize:     info.Size,
		TempDir:      tempDir,
	}
	paramsJSON, _ := json.Marshal(params)

	_, err = model.CreateJob(session.UserID, jobID, "transcode", string(paramsJSON))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "create_job_failed"})
	}

	return c.JSON(fiber.Map{"job_id": jobID})
}

// RunTranscodeJob is the dispatcher handler for transcode jobs.
func (h *Handler) RunTranscodeJob(ctx context.Context, job *model.Job) error {
	var params TranscodeParams
	if err := json.Unmarshal([]byte(job.Params), &params); err != nil {
		return fmt.Errorf("parse params: %w", err)
	}

	// Create temp dir
	if err := os.MkdirAll(params.TempDir, 0755); err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(params.TempDir) }()

	inputExt := filepath.Ext(params.OriginalName)
	inputPath := filepath.Join(params.TempDir, "input"+inputExt)
	outputExt := service.OutputExtension(params.OutputFormat)
	outputPath := filepath.Join(params.TempDir, "output"+outputExt)

	// Phase 1: Download and decrypt
	_ = model.UpdateJobProgress(job.JobID, 0.0, "downloading")
	if err := h.Store.DownloadToFile(params.SourceKey, inputPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	key, err := auth.DeriveKey(h.Config.Server.EncryptionSecret, job.UserID)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}
	decPath := inputPath + ".dec"
	if err := auth.DecryptFile(key, inputPath, decPath); err != nil {
		return fmt.Errorf("decrypt source: %w", err)
	}
	_ = os.Remove(inputPath)
	if err := os.Rename(decPath, inputPath); err != nil {
		return fmt.Errorf("rename decrypted: %w", err)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 2: Probe + Transcode
	_ = model.UpdateJobProgress(job.JobID, 0.1, "transcoding")
	transcoder := service.NewTranscoder(h.Config.Transcode)

	probe, err := transcoder.Probe(ctx, inputPath)
	if err != nil {
		log.Printf("[transcode] probe failed for job %s: %v (continuing without duration)", job.JobID, err)
	}

	err = transcoder.Run(ctx, inputPath, outputPath, params.MediaType, params.Preset, params.OutputFormat, probe, func(pct float64) {
		// Map 0~1 to 0.1~0.8
		progress := 0.1 + pct*0.7
		_ = model.UpdateJobProgress(job.JobID, progress, "transcoding")
	})
	if err != nil {
		return fmt.Errorf("transcode: %w", err)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 3: Upload to OSS
	_ = model.UpdateJobProgress(job.JobID, 0.85, "uploading_oss")

	// If replacing and format changed, delete old file first
	if params.Replace && params.TargetKey != params.SourceKey {
		_ = h.Store.DeleteObject(params.SourceKey)
	}

	// Get output size before encryption (plaintext size)
	stat, _ := os.Stat(outputPath)
	var outputSize int64
	if stat != nil {
		outputSize = stat.Size()
	}

	// Encrypt output and upload
	encPath := outputPath + ".enc"
	if err := auth.EncryptFile(key, outputPath, encPath); err != nil {
		return fmt.Errorf("encrypt output: %w", err)
	}
	if err := h.Store.UploadFromFile(params.TargetKey, encPath); err != nil {
		_ = os.Remove(encPath)
		return fmt.Errorf("upload: %w", err)
	}
	_ = os.Remove(encPath)

	// Update file record
	ct := mime.TypeByExtension(outputExt)
	_ = model.UpsertFile(job.UserID, params.TargetKey, params.OutputName, false, outputSize, ct, "")

	// If replacing with different extension, delete old file record and OSS object
	if params.Replace && params.TargetKey != params.SourceKey {
		_ = model.DeleteFile(job.UserID, params.SourceKey)
	}

	result := TranscodeResult{
		OutputSize: outputSize,
		DurationMs: probeDurationMs(probe),
	}
	resultJSON, _ := json.Marshal(result)
	_ = model.UpdateJobResult(job.JobID, string(resultJSON))

	return nil
}

func probeDurationMs(p *service.ProbeResult) int64 {
	if p != nil {
		return int64(p.Duration * 1000)
	}
	return 0
}
