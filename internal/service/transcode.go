package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"zephyr/config"
)

type ProbeResult struct {
	Duration  float64 `json:"duration"`
	VideoCode string  `json:"video_codec"`
	AudioCode string  `json:"audio_codec"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
}

type Transcoder struct {
	FFmpegPath  string
	FFprobePath string
}

func NewTranscoder(cfg config.TranscodeConfig) *Transcoder {
	return &Transcoder{
		FFmpegPath:  cfg.FFmpegPath,
		FFprobePath: cfg.FFprobePath,
	}
}

func (t *Transcoder) Probe(ctx context.Context, inputPath string) (*ProbeResult, error) {
	cmd := exec.CommandContext(ctx, t.FFprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse probe: %w", err)
	}

	result := &ProbeResult{}
	result.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			result.VideoCode = s.CodecName
			result.Width = s.Width
			result.Height = s.Height
		case "audio":
			result.AudioCode = s.CodecName
		}
	}
	return result, nil
}

// videoPreset returns video-only ffmpeg arguments for a given quality preset and output format.
func videoPreset(preset, outputFormat string) []string {
	switch outputFormat {
	case "webm":
		switch preset {
		case "low":
			return []string{"-c:v", "libvpx-vp9", "-crf", "40", "-b:v", "0", "-cpu-used", "4"}
		case "high":
			return []string{"-c:v", "libvpx-vp9", "-crf", "24", "-b:v", "0", "-cpu-used", "1"}
		default:
			return []string{"-c:v", "libvpx-vp9", "-crf", "32", "-b:v", "0", "-cpu-used", "2"}
		}
	default: // mp4, mkv, mov, etc.
		switch preset {
		case "low":
			return []string{"-c:v", "libx264", "-crf", "28", "-preset", "fast"}
		case "high":
			return []string{"-c:v", "libx264", "-crf", "18", "-preset", "slow"}
		default:
			return []string{"-c:v", "libx264", "-crf", "23", "-preset", "medium"}
		}
	}
}

// audioPreset returns audio-only ffmpeg arguments for a given quality preset and output format.
func audioPreset(preset, outputFormat string) []string {
	switch outputFormat {
	case "webm", "ogg":
		switch preset {
		case "low":
			return []string{"-c:a", "libopus", "-b:a", "96k"}
		case "high":
			return []string{"-c:a", "libopus", "-b:a", "256k"}
		default:
			return []string{"-c:a", "libopus", "-b:a", "128k"}
		}
	default:
		switch preset {
		case "low":
			return []string{"-c:a", "aac", "-b:a", "128k"}
		case "high":
			return []string{"-c:a", "aac", "-b:a", "320k"}
		default:
			return []string{"-c:a", "aac", "-b:a", "192k"}
		}
	}
}

// imagePreset returns image ffmpeg arguments for a given quality preset.
func imagePreset(preset string) []string {
	switch preset {
	case "low":
		return []string{"-quality", "50"}
	case "high":
		return []string{"-quality", "90"}
	default:
		return []string{"-quality", "75"}
	}
}

// BuildArgs constructs the full ffmpeg command arguments.
// probe is used to decide whether to include audio encoding args.
func (t *Transcoder) BuildArgs(inputPath, outputPath, mediaType, preset, outputFormat string, probe *ProbeResult) []string {
	args := []string{"-i", inputPath, "-y"}

	switch mediaType {
		case "video":
			args = append(args, videoPreset(preset, outputFormat)...)
			if probe != nil && probe.AudioCode != "" {
				args = append(args, audioPreset(preset, outputFormat)...)
			} else {
				args = append(args, "-an")
			}
		case "audio":
			args = append(args, audioPreset(preset, outputFormat)...)
		case "image":
		args = append(args, imagePreset(preset)...)
	}

	// progress to stdout (pipe:1), stderr stays for error messages
	args = append(args, "-progress", "pipe:1")
	args = append(args, outputPath)
	return args
}

// progressRe matches "out_time_ms=123456" lines from ffmpeg progress output
var progressRe = regexp.MustCompile(`out_time_ms=(\d+)`)

// Run executes ffmpeg with progress reporting. onProgress receives 0.0~1.0.
func (t *Transcoder) Run(ctx context.Context, inputPath, outputPath, mediaType, preset, outputFormat string, probe *ProbeResult, onProgress func(float64)) error {
	args := t.BuildArgs(inputPath, outputPath, mediaType, preset, outputFormat, probe)
	cmd := exec.CommandContext(ctx, t.FFmpegPath, args...)

	// Read progress from stdout, keep stderr separate for error messages
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if matches := progressRe.FindStringSubmatch(line); len(matches) == 2 {
			ms, _ := strconv.ParseFloat(matches[1], 64)
			durationSec := 0.0
			if probe != nil {
				durationSec = probe.Duration
			}
			if durationSec > 0 && onProgress != nil {
				pct := (ms / 1_000_000) / durationSec
				if pct > 1.0 {
					pct = 1.0
				}
				onProgress(pct)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%w\nffmpeg stderr:\n%s", err, stderrBuf.String())
	}
	return nil
}

// OutputExtension returns the file extension for a given output format.
func OutputExtension(format string) string {
	switch strings.ToLower(format) {
	case "mp4":
		return ".mp4"
	case "webm":
		return ".webm"
	case "mkv":
		return ".mkv"
	case "mov":
		return ".mov"
	case "mp3":
		return ".mp3"
	case "aac":
		return ".aac"
	case "flac":
		return ".flac"
	case "ogg":
		return ".ogg"
	case "webp":
		return ".webp"
	case "jpg", "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	default:
		return "." + strings.ToLower(format)
	}
}

// DetectMediaType guesses media type from file extension.
func DetectMediaType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp4", ".mkv", ".webm", ".mov", ".avi", ".flv", ".wmv", ".m4v", ".ts":
		return "video"
	case ".mp3", ".aac", ".flac", ".ogg", ".wav", ".wma", ".m4a", ".opus":
		return "audio"
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif", ".tiff", ".tif", ".heic", ".heif", ".avif":
		return "image"
	default:
		return ""
	}
}
