package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestGeneratePDFThumbnailOnServer(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	renderer, err := exec.LookPath("pdftoppm")
	if err != nil {
		t.Skip("pdftoppm is not installed")
	}
	dir := t.TempDir()
	stream := "0 0 1 rg 20 20 380 160 re f"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 200] /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	source, output := filepath.Join(dir, "document"), filepath.Join(dir, "thumbnail.webp")
	if err := os.WriteFile(source, []byte(pdf.String()), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = GenerateThumbnail(t.Context(), ThumbnailOptions{FFmpegPath: ffmpeg, FFprobePath: ffprobe, PDFToPPMPath: renderer, ContentType: "application/pdf", MaxDimension: 480}, source, output)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Probe(t.Context(), ffprobe, output)
	if err != nil || info.Width > 480 || info.Height > 480 || info.Width <= 0 {
		t.Fatalf("invalid server PDF preview: %#v %v", info, err)
	}
	if _, err := os.Stat(output + ".page.png"); !os.IsNotExist(err) {
		t.Fatal("PDF scratch image was retained")
	}
}

func TestStillImageThumbnailDoesNotSeekPastItsOnlyFrame(t *testing.T) {
	ffmpeg, ffprobe := requireFFmpeg(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "still.gif")
	makeFixture(t, ffmpeg, source, "-f", "lavfi", "-i", "color=c=red:s=120x90", "-frames:v", "1")
	if _, err := GenerateThumbnail(t.Context(), ThumbnailOptions{FFmpegPath: ffmpeg, FFprobePath: ffprobe, ContentType: "image/gif", MaxDimension: 480}, source, filepath.Join(dir, "thumb.webp")); err != nil {
		t.Fatal(err)
	}
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
