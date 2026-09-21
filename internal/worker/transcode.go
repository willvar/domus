package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// TranscodeProfile is one fixed playback-quality preset exposed by the
// quality menu. The first release keeps parameters server-defined so the
// client only names a profile.
type TranscodeProfile struct {
	ID           string
	Height       int
	CRF          int
	AudioBitrate int
}

// TranscodeProfiles lists the quality menu presets in display order.
func TranscodeProfiles() []TranscodeProfile {
	return []TranscodeProfile{
		{ID: "2160p", Height: 2160, CRF: 23, AudioBitrate: 128},
		{ID: "1440p", Height: 1440, CRF: 23, AudioBitrate: 128},
		{ID: "1080p", Height: 1080, CRF: 23, AudioBitrate: 128},
		{ID: "720p", Height: 720, CRF: 23, AudioBitrate: 128},
		{ID: "480p", Height: 480, CRF: 23, AudioBitrate: 128},
	}
}

// FindTranscodeProfile resolves a profile ID. The second return is false for
// unknown profiles.
func FindTranscodeProfile(id string) (TranscodeProfile, bool) {
	for _, profile := range TranscodeProfiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return TranscodeProfile{}, false
}

// TranscodeOptions controls one transcode job.
type TranscodeOptions struct {
	FFmpegPath     string
	FFprobePath    string
	Profile        TranscodeProfile
	SegmentSeconds float64
}

// PlaylistSegment is one completed entry of the growing ffmpeg HLS playlist.
type PlaylistSegment struct {
	URI      string
	Duration float64
}

// ParseTranscodePlaylist extracts the ordered media segments of an event
// playlist. Init segments and unsupported lines are ignored; entries without
// a positive duration are skipped.
func ParseTranscodePlaylist(content string) []PlaylistSegment {
	segments := make([]PlaylistSegment, 0, 16)
	scanner := bufio.NewScanner(strings.NewReader(content))
	var current *PlaylistSegment
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			raw := strings.TrimPrefix(line, "#EXTINF:")
			if index := strings.Index(raw, ","); index >= 0 {
				raw = raw[:index]
			}
			duration, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || duration <= 0 {
				continue
			}
			segments = append(segments, PlaylistSegment{Duration: duration})
			current = &segments[len(segments)-1]
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if current != nil {
			current.URI = line
			current = nil
		}
	}
	result := make([]PlaylistSegment, 0, len(segments))
	for _, segment := range segments {
		if segment.URI == "" {
			continue
		}
		result = append(result, segment)
	}
	return result
}

// TranscodeRun is a running ffmpeg transcode producing fMP4/HLS segments in a
// scratch directory. Init segment, playlist and segment files appear there as
// the job progresses.
type TranscodeRun struct {
	command      *exec.Cmd
	scratchDir   string
	playlistPath string
	cancel       context.CancelFunc
	done         chan struct{}
	waitErr      error
	stderr       bytes.Buffer
}

// StartTranscode launches ffmpeg. The returned run owns the process; Wait
// blocks until completion and Cancel aborts it. The source is read from
// plainPath (a DOFS FUSE mount) and output lands in scratchDir.
func StartTranscode(ctx context.Context, options TranscodeOptions, source, scratchDir string) (*TranscodeRun, error) {
	if strings.TrimSpace(options.FFmpegPath) == "" {
		return nil, errors.New("worker: ffmpeg path is empty")
	}
	if options.Profile.Height <= 0 {
		return nil, errors.New("worker: transcode profile height must be positive")
	}
	segmentSeconds := options.SegmentSeconds
	if segmentSeconds <= 0 {
		segmentSeconds = 4
	}
	if err := os.MkdirAll(scratchDir, 0700); err != nil {
		return nil, fmt.Errorf("worker: transcode scratch directory: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	scale := fmt.Sprintf("scale=-2:'min(%d,ih)'", options.Profile.Height)
	forcedKeyframes := fmt.Sprintf("expr:gte(t,n_forced*%s)", strconv.FormatFloat(segmentSeconds, 'f', 2, 64))
	arguments := []string{
		"-nostdin", "-v", "error", "-y",
		"-i", source,
		"-vf", scale,
		"-force_key_frames", forcedKeyframes,
		"-c:v", "libx264", "-preset", "veryfast",
		"-crf", strconv.Itoa(options.Profile.CRF),
		"-c:a", "aac", "-b:a", strconv.Itoa(options.Profile.AudioBitrate) + "k",
		"-f", "hls",
		"-hls_time", strconv.FormatFloat(segmentSeconds, 'f', 2, 64),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "temp_file",
		"-hls_segment_filename", filepath.Join(scratchDir, "seg%03d.m4s"),
		filepath.Join(scratchDir, "index.m3u8"),
	}
	command := exec.CommandContext(runCtx, options.FFmpegPath, arguments...)
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("worker: ffmpeg transcode %s: %w", source, err)
	}
	run := &TranscodeRun{
		command: command, scratchDir: scratchDir, cancel: cancel,
		playlistPath: filepath.Join(scratchDir, "index.m3u8"),
		done:         make(chan struct{}),
	}
	command.Stderr = &run.stderr
	go func() {
		run.waitErr = run.command.Wait()
		close(run.done)
	}()
	return run, nil
}

// Wait blocks until ffmpeg exits. A context cancellation (job cancelled by the
// web tier) is reported as an unclean exit like any ffmpeg failure.
func (run *TranscodeRun) Wait() error {
	<-run.done
	return run.waitErr
}

// Done reports whether the process has exited.
func (run *TranscodeRun) Done() <-chan struct{} { return run.done }

// Cancel kills the ffmpeg process. It is safe to call more than once.
func (run *TranscodeRun) Cancel() { run.cancel() }

// PlaylistPath returns the path of the growing HLS playlist.
func (run *TranscodeRun) PlaylistPath() string { return run.playlistPath }

// InitSegmentPath returns the path of the fMP4 init segment.
func (run *TranscodeRun) InitSegmentPath() string {
	return filepath.Join(run.scratchDir, "init.mp4")
}

// SegmentPath resolves the playlist URI of a segment inside the scratch dir.
func (run *TranscodeRun) SegmentPath(uri string) string {
	return filepath.Join(run.scratchDir, filepath.Base(uri))
}

// avcProfileIDs maps H.264 profile names to their AVCCodec profile byte.
var avcProfileByte = map[string]byte{
	"baseline": 0x42, "constrained baseline": 0x42,
	"main": 0x4D, "high": 0x64,
}

// ProbeTranscodeCodecs inspects an fMP4 init segment and returns the MSE
// codecs string (for example "avc1.640028,mp4a.40.2") plus the coded
// dimensions. ffprobe cannot always recover the H.264 level from a bare fMP4
// init segment (it reports -99), so implausible levels are clamped to a
// broadly compatible hint instead of producing an unparseable string.
func ProbeTranscodeCodecs(ctx context.Context, ffprobePath, initSegment string) (codecs string, width, height int, err error) {
	if strings.TrimSpace(ffprobePath) == "" {
		return "", 0, 0, errors.New("worker: ffprobe path is empty")
	}
	command := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "stream=index,codec_name,profile,level,width,height",
		"-of", "json",
		initSegment,
	)
	output, err := command.Output()
	if err != nil {
		return "", 0, 0, fmt.Errorf("worker: ffprobe init segment %s: %w%s", initSegment, err, commandStderr(err))
	}
	var parsed struct {
		Streams []struct {
			Index     int    `json:"index"`
			CodecName string `json:"codec_name"`
			Profile   string `json:"profile"`
			Level     int    `json:"level"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return "", 0, 0, fmt.Errorf("worker: decode ffprobe output for %s: %w", initSegment, err)
	}
	parts := make([]string, 0, 2)
	for _, stream := range parsed.Streams {
		switch stream.CodecName {
		case "h264":
			profileByte, ok := avcProfileByte[strings.ToLower(stream.Profile)]
			if !ok {
				profileByte = 0x64
			}
			// Valid AVC level_idc runs 10..62; probe gaps (e.g. -99 from an
			// init-only SPS) fall back to a level the coded size actually
			// requires, so a 4K transcode never advertises a 1080p level.
			level := byte(stream.Level)
			if stream.Level <= 0 || stream.Level > 62 {
				switch {
				case stream.Height >= 1440:
					level = 0x33 // 5.1
				case stream.Height >= 1080:
					level = 0x2A // 4.2
				default:
					level = 0x28 // 4.0
				}
			}
			video := fmt.Sprintf("avc1.%02X%02X%02X", profileByte, 0x00, level)
			parts = append(parts, video)
			if stream.Width > 0 {
				width, height = stream.Width, stream.Height
			}
		case "aac":
			parts = append(parts, "mp4a.40.2")
		}
	}
	if len(parts) == 0 {
		return "", 0, 0, fmt.Errorf("worker: no h264/aac stream in %s", initSegment)
	}
	return strings.Join(parts, ","), width, height, nil
}
