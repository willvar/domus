// Package worker implements the optional desktop-side content worker. It
// reads source files from a plain filesystem path (the DOFS FUSE mount) and
// produces derived artifacts (currently WebP thumbnails) that are written
// back through the same mount. It never touches encryption keys or object
// storage directly.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxThumbnailBytes mirrors the browser thumbnail limit enforced by the
// upload completion handler for client-generated thumbnails.
const MaxThumbnailBytes = 4 * 1024 * 1024

// DefaultThumbQuality mirrors the browser canvas encoder (0.8).
const DefaultThumbQuality = 80

// ThumbnailOptions controls thumbnail generation.
type ThumbnailOptions struct {
	FFmpegPath        string
	FFprobePath       string
	MaxDimension      int
	Quality           int
	ProbeTimeout      time.Duration
	GenerationTimeout time.Duration
}

// MediaInfo carries the probe results of the source file. Duration is zero
// for still images.
type MediaInfo struct {
	Width    int
	Height   int
	Duration float64
}

type probeOutput struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe returns the first video-stream geometry and container duration of the
// source file. Images report the decoder frame geometry with zero duration.
func Probe(ctx context.Context, ffprobePath, source string) (MediaInfo, error) {
	if strings.TrimSpace(ffprobePath) == "" {
		return MediaInfo{}, errors.New("worker: ffprobe path is empty")
	}
	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(probeCtx, ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration",
		"-of", "json",
		source,
	)
	output, err := command.Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("worker: ffprobe %s: %w%s", source, err, commandStderr(err))
	}
	var parsed probeOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return MediaInfo{}, fmt.Errorf("worker: decode ffprobe output for %s: %w", source, err)
	}
	if len(parsed.Streams) == 0 || parsed.Streams[0].Width <= 0 || parsed.Streams[0].Height <= 0 {
		return MediaInfo{}, fmt.Errorf("worker: no usable video stream in %s", source)
	}
	info := MediaInfo{Width: parsed.Streams[0].Width, Height: parsed.Streams[0].Height}
	if parsed.Format.Duration != "" && parsed.Format.Duration != "N/A" {
		if duration, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil && duration > 0 {
			info.Duration = duration
		}
	}
	return info, nil
}

// GenerateThumbnail writes a WebP thumbnail of the source file to output.
// It returns the source media information (original dimensions and duration)
// so callers can persist the same metadata the browser path records.
func GenerateThumbnail(ctx context.Context, options ThumbnailOptions, source, output string) (MediaInfo, error) {
	if strings.TrimSpace(options.FFmpegPath) == "" {
		return MediaInfo{}, errors.New("worker: ffmpeg path is empty")
	}
	if options.MaxDimension <= 0 {
		return MediaInfo{}, errors.New("worker: thumbnail max dimension must be positive")
	}
	quality := options.Quality
	if quality <= 0 || quality > 100 {
		quality = DefaultThumbQuality
	}
	info, err := Probe(ctx, options.FFprobePath, source)
	if err != nil {
		return MediaInfo{}, err
	}

	timeout := options.GenerationTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	generateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	arguments := []string{"-nostdin", "-v", "error", "-y"}
	if info.Duration > 0 {
		seek := info.Duration / 2
		if seek > 1 {
			seek = 1
		}
		arguments = append(arguments, "-ss", strconv.FormatFloat(seek, 'f', 3, 64))
	}
	scale := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", options.MaxDimension, options.MaxDimension)
	arguments = append(arguments,
		"-i", source,
		"-frames:v", "1",
		"-vf", scale,
		"-c:v", "libwebp",
		"-quality", strconv.Itoa(quality),
		"-f", "webp",
		output,
	)
	command := exec.CommandContext(generateCtx, options.FFmpegPath, arguments...)
	if err := command.Run(); err != nil {
		return MediaInfo{}, fmt.Errorf("worker: ffmpeg thumbnail %s: %w%s", source, err, commandStderr(err))
	}
	stat, err := os.Stat(output)
	if err != nil {
		return MediaInfo{}, fmt.Errorf("worker: thumbnail output %s: %w", output, err)
	}
	if stat.Size() <= 0 {
		return MediaInfo{}, fmt.Errorf("worker: thumbnail output %s is empty", output)
	}
	if stat.Size() > MaxThumbnailBytes {
		return MediaInfo{}, fmt.Errorf("worker: thumbnail output %s exceeds %d bytes", filepath.Base(output), MaxThumbnailBytes)
	}
	return info, nil
}

func commandStderr(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSpace(string(exitErr.Stderr))
		if message != "" {
			return ": " + message
		}
	}
	return ""
}

// CommandStderr extracts the trailing ffmpeg/ffprobe stderr from a command
// error. It is the exported form used by callers outside this package.
func CommandStderr(err error) string { return commandStderr(err) }
