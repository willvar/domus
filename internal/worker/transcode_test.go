package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTranscodePlaylist(t *testing.T) {
	content := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		"#EXT-X-TARGETDURATION:4",
		"#EXT-X-MEDIA-SEQUENCE:0",
		"#EXT-X-PLAYLIST-TYPE:EVENT",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXTINF:4.000000,",
		"seg000.m4s",
		"#EXTINF:3.500000,",
		"seg001.m4s",
		"#EXT-X-ENDLIST",
	}, "\n")
	segments := ParseTranscodePlaylist(content)
	if len(segments) != 2 {
		t.Fatalf("segments = %#v, want 2", segments)
	}
	if segments[0].URI != "seg000.m4s" || segments[0].Duration != 4 {
		t.Fatalf("segment[0] = %#v", segments[0])
	}
	if segments[1].URI != "seg001.m4s" || segments[1].Duration != 3.5 {
		t.Fatalf("segment[1] = %#v", segments[1])
	}
	if got := ParseTranscodePlaylist("#EXTM3U\n"); len(got) != 0 {
		t.Fatalf("empty playlist produced %#v", got)
	}
}

func TestFindTranscodeProfile(t *testing.T) {
	profile, ok := FindTranscodeProfile("720p")
	if !ok || profile.Height != 720 || profile.CRF != 23 || profile.AudioBitrate != 128 {
		t.Fatalf("profile 720p = %#v, %v", profile, ok)
	}
	if _, ok := FindTranscodeProfile("4k"); ok {
		t.Fatal("unknown profile accepted")
	}
}

func TestStartTranscodeProducesIncrementalSegments(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	// 6-second 320x180 test clip.
	makeFixture(t, ffmpeg, source,
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=10:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-pix_fmt", "yuv420p")

	scratch := filepath.Join(dir, "scratch")
	run, err := StartTranscode(context.Background(), TranscodeOptions{
		FFmpegPath: ffmpeg, FFprobePath: ffprobe, Profile: TranscodeProfile{ID: "480p", Height: 480, CRF: 23, AudioBitrate: 128},
	}, source, scratch)
	if err != nil {
		t.Fatalf("StartTranscode: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	sawGrowth := false
	lastCount := 0
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(run.PlaylistPath())
		if err == nil {
			if count := len(ParseTranscodePlaylist(string(content))); count > lastCount {
				lastCount = count
				sawGrowth = true
			}
		}
		select {
		case <-run.Done():
			if !sawGrowth {
				t.Fatal("transcode finished without incremental segment visibility")
			}
			final := ParseTranscodePlaylist(mustRead(t, run.PlaylistPath()))
			if len(final) < 2 {
				t.Fatalf("final playlist has %d segments, want >= 2", len(final))
			}
			if _, err := os.Stat(run.InitSegmentPath()); err != nil {
				t.Fatalf("init segment missing: %v", err)
			}
			codecs, width, height, err := ProbeTranscodeCodecs(context.Background(), ffprobe, run.InitSegmentPath())
			if err != nil {
				t.Fatalf("ProbeTranscodeCodecs: %v", err)
			}
			if !strings.Contains(codecs, "avc1.") {
				t.Fatalf("codecs = %q, want avc1 prefix", codecs)
			}
			if width == 0 || height == 0 {
				t.Fatalf("init segment probe returned %dx%d", width, height)
			}
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
	run.Cancel()
	t.Fatal("transcode did not finish in time")
}

func countSegmentFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "seg") && !strings.HasSuffix(entry.Name(), ".tmp") {
			count++
		}
	}
	return count
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCancelTranscodeRun(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	// A 60-second clip so the transcode is still running when we cancel.
	if out, err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=160x90:rate=10:duration=60",
		"-c:v", "libx264", "-preset", "ultrafast", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture failed: %v: %s", err, out)
	}
	run, err := StartTranscode(context.Background(), TranscodeOptions{
		FFmpegPath: ffmpeg, FFprobePath: ffprobe, Profile: TranscodeProfile{ID: "480p", Height: 480, CRF: 23, AudioBitrate: 128},
	}, source, filepath.Join(dir, "scratch"))
	if err != nil {
		t.Fatal(err)
	}
	run.Cancel()
	if err := run.Wait(); err == nil {
		t.Fatal("cancelled run exited cleanly")
	}
}
