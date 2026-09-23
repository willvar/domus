package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// MediaMeta is the ffprobe-derived summary of a media source file (container
// and stream properties plus common recording tags). It is stored as JSON by
// the worker and shown as file metadata in the UI.
type MediaMeta struct {
	Duration        float64 `json:"duration,omitempty"`
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	FPS             float64 `json:"fps,omitempty"`
	BitRate         int64   `json:"bit_rate,omitempty"`
	Rotation        int     `json:"rotation,omitempty"`
	AudioChannels   int     `json:"audio_channels,omitempty"`
	AudioSampleRate int     `json:"audio_sample_rate,omitempty"`
	CreationTime    string  `json:"creation_time,omitempty"`
	Make            string  `json:"make,omitempty"`
	Model           string  `json:"model,omitempty"`
	Title           string  `json:"title,omitempty"`
	Artist          string  `json:"artist,omitempty"`
	Album           string  `json:"album,omitempty"`
	Genre           string  `json:"genre,omitempty"`
	Date            string  `json:"date,omitempty"`
	GPS             string  `json:"gps,omitempty"`
}

func (m MediaMeta) IsEmpty() bool {
	return m == MediaMeta{}
}

// flexInt tolerates ffprobe's inconsistent scalar encoding: the same stream
// field may arrive as a JSON number or a quoted string depending on version.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	trimmed := strings.Trim(string(data), `"`)
	*f = 0
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if value, err := strconv.Atoi(trimmed); err == nil {
		*f = flexInt(value)
	}
	return nil
}

type mediaMetaStream struct {
	CodecType    string  `json:"codec_type"`
	Width        flexInt `json:"width"`
	Height       flexInt `json:"height"`
	RFrameRate   string  `json:"r_frame_rate"`
	Channels     flexInt `json:"channels"`
	SampleRate   flexInt `json:"sample_rate"`
	SideDataList []struct {
		Rotation flexInt `json:"rotation"`
	} `json:"side_data_list"`
	Tags struct {
		Rotate string `json:"rotate"`
	} `json:"tags"`
}

type mediaMetaFormat struct {
	Duration string            `json:"duration"`
	BitRate  string            `json:"bit_rate"`
	Tags     map[string]string `json:"tags"`
}

type mediaMetaOutput struct {
	Streams []mediaMetaStream `json:"streams"`
	Format  mediaMetaFormat   `json:"format"`
}

// ProbeMediaMeta runs one ffprobe pass over the source file and reduces it to
// a compact metadata summary. It never fails on missing fields; only a
// container that has no streams at all (or a broken probe) errors out.
func ProbeMediaMeta(ctx context.Context, ffprobePath, source string) (MediaMeta, error) {
	if strings.TrimSpace(ffprobePath) == "" {
		return MediaMeta{}, errors.New("worker: ffprobe path is empty")
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
		"-show_entries",
		"stream=codec_type,width,height,r_frame_rate,channels,sample_rate:stream_side_data=rotation:stream_tags=rotate:format=duration,bit_rate:format_tags=make,model,title,artist,album,genre,date,creation_time,location",
		"-of", "json",
		source,
	)
	output, err := command.Output()
	if err != nil {
		return MediaMeta{}, fmt.Errorf("worker: ffprobe %s: %w%s", source, err, commandStderr(err))
	}
	var parsed mediaMetaOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return MediaMeta{}, fmt.Errorf("worker: decode ffprobe output for %s: %w", source, err)
	}
	if len(parsed.Streams) == 0 {
		return MediaMeta{}, fmt.Errorf("worker: no streams in %s", source)
	}

	meta := MediaMeta{}
	var haveVideo, haveAudio bool
	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			if haveVideo {
				continue
			}
			haveVideo = true
			meta.Width, meta.Height = int(stream.Width), int(stream.Height)
			meta.FPS = parseFrameRate(stream.RFrameRate)
			meta.Rotation = parseMetaRotation(stream)
		case "audio":
			if haveAudio {
				continue
			}
			haveAudio = true
			meta.AudioChannels, meta.AudioSampleRate = int(stream.Channels), int(stream.SampleRate)
		}
	}
	if !haveVideo && !haveAudio {
		return MediaMeta{}, fmt.Errorf("worker: no media streams in %s", source)
	}
	if parsed.Format.Duration != "" && parsed.Format.Duration != "N/A" {
		if duration, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
			meta.Duration = duration
		}
	}
	if parsed.Format.BitRate != "" && parsed.Format.BitRate != "N/A" {
		if bitRate, err := strconv.ParseInt(parsed.Format.BitRate, 10, 64); err == nil {
			meta.BitRate = bitRate
		}
	}
	for key, target := range map[string]*string{
		"make":          &meta.Make,
		"model":         &meta.Model,
		"title":         &meta.Title,
		"artist":        &meta.Artist,
		"album":         &meta.Album,
		"genre":         &meta.Genre,
		"date":          &meta.Date,
		"creation_time": &meta.CreationTime,
		"location":      &meta.GPS,
	} {
		*target = strings.TrimSpace(parsed.Format.Tags[key])
	}
	if meta.CreationTime == "" {
		meta.CreationTime = meta.Date
	}
	return meta, nil
}

// parseMetaRotation prefers the modern Display Matrix side data and falls
// back to the legacy "rotate" stream tag.
func parseMetaRotation(stream mediaMetaStream) int {
	for _, side := range stream.SideDataList {
		if side.Rotation != 0 {
			return int(side.Rotation)
		}
	}
	if rotation, err := strconv.Atoi(strings.TrimSpace(stream.Tags.Rotate)); err == nil {
		return rotation
	}
	return 0
}

// parseFrameRate evaluates an ffprobe rational such as "30000/1001".
func parseFrameRate(rate string) float64 {
	parts := strings.SplitN(strings.TrimSpace(rate), "/", 2)
	if parts[0] == "" || parts[0] == "0" {
		return 0
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) == 1 {
		return numerator
	}
	denominator, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || denominator == 0 {
		return numerator
	}
	value := numerator / denominator
	// Round to two decimals to keep 29.97 out of float noise.
	return float64(int(value*100+0.5)) / 100
}
