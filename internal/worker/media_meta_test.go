package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProbeMediaMeta(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-metadata", "title=Probe Test", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture failed: %v: %s", err, out)
	}
	meta, err := ProbeMediaMeta(context.Background(), ffprobe, source)
	if err != nil {
		t.Fatalf("ProbeMediaMeta: %v", err)
	}
	if meta.Width != 320 || meta.Height != 180 {
		t.Fatalf("unexpected geometry %dx%d", meta.Width, meta.Height)
	}
	if meta.Duration < 1.5 || meta.Duration > 2.5 {
		t.Fatalf("unexpected duration %v", meta.Duration)
	}
	if meta.FPS != 30 {
		t.Fatalf("unexpected fps %v", meta.FPS)
	}
	if meta.AudioChannels != 2 && meta.AudioChannels != 1 {
		t.Fatalf("unexpected audio channels %v", meta.AudioChannels)
	}
	if meta.Title != "Probe Test" {
		t.Fatalf("unexpected title %q", meta.Title)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal(err)
	}
}
