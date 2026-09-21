package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	return ffmpeg, ffprobe
}

func makeFixture(t *testing.T, ffmpeg, output string, args ...string) {
	t.Helper()
	command := exec.Command(ffmpeg, append([]string{"-nostdin", "-v", "error", "-y"}, append(args, output)...)...)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture %s failed: %v: %s", output, err, out)
	}
}

func TestGenerateThumbnailImage(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.png")
	makeFixture(t, ffmpeg, source, "-f", "lavfi", "-i", "color=c=red:s=960x540", "-frames:v", "1")

	output := filepath.Join(dir, "thumb.webp")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	info, err := GenerateThumbnail(ctx, ThumbnailOptions{
		FFmpegPath: ffmpeg, FFprobePath: ffprobe, MaxDimension: 480,
	}, source, output)
	if err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	if info.Width != 960 || info.Height != 540 {
		t.Fatalf("source media info = %dx%d, want 960x540", info.Width, info.Height)
	}
	stat, err := os.Stat(output)
	if err != nil || stat.Size() == 0 {
		t.Fatalf("thumbnail output missing: %v", err)
	}
	if stat.Size() > MaxThumbnailBytes {
		t.Fatalf("thumbnail %d bytes exceeds limit", stat.Size())
	}
	thumbInfo, err := Probe(ctx, ffprobe, output)
	if err != nil {
		t.Fatalf("Probe(thumbnail): %v", err)
	}
	if thumbInfo.Width != 480 || thumbInfo.Height != 270 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 480x270", thumbInfo.Width, thumbInfo.Height)
	}
}

func TestGenerateThumbnailDoesNotUpscale(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "small.png")
	makeFixture(t, ffmpeg, source, "-f", "lavfi", "-i", "color=c=blue:s=120x90", "-frames:v", "1")

	output := filepath.Join(dir, "thumb.webp")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := GenerateThumbnail(ctx, ThumbnailOptions{
		FFmpegPath: ffmpeg, FFprobePath: ffprobe, MaxDimension: 480,
	}, source, output); err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	thumbInfo, err := Probe(ctx, ffprobe, output)
	if err != nil {
		t.Fatalf("Probe(thumbnail): %v", err)
	}
	if thumbInfo.Width != 120 || thumbInfo.Height != 90 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 120x90", thumbInfo.Width, thumbInfo.Height)
	}
}

func TestGenerateThumbnailVideoSeeksNearOneSecond(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	makeFixture(t, ffmpeg, source,
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=10:duration=3",
		"-pix_fmt", "yuv420p",
	)

	output := filepath.Join(dir, "thumb.webp")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	info, err := GenerateThumbnail(ctx, ThumbnailOptions{
		FFmpegPath: ffmpeg, FFprobePath: ffprobe, MaxDimension: 480,
	}, source, output)
	if err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	if info.Width != 640 || info.Height != 360 {
		t.Fatalf("source media info = %dx%d, want 640x360", info.Width, info.Height)
	}
	if info.Duration < 2.5 || info.Duration > 3.5 {
		t.Fatalf("source duration = %f, want ~3s", info.Duration)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("thumbnail output missing: %v", err)
	}
}
