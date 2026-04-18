package handler

import (
	"math"
	"testing"

	"zephyr/config"
)

func TestEncryptedMultipartPartCount(t *testing.T) {
	t.Run("empty file still uses one part", func(t *testing.T) {
		if got := encryptedMultipartPartCount(0, config.UploadChunkSize); got != 1 {
			t.Fatalf("expected 1 part, got %d", got)
		}
	})

	t.Run("matches simple ceil when boundaries line up", func(t *testing.T) {
		plainSize := int64(1024)
		encryptedSize := encryptedFileSize(plainSize)
		want := int(math.Ceil(float64(encryptedSize) / float64(config.UploadChunkSize)))
		if got := encryptedMultipartPartCount(plainSize, config.UploadChunkSize); got != want {
			t.Fatalf("expected %d parts, got %d", want, got)
		}
	})

	t.Run("accounts for chunk alignment slack", func(t *testing.T) {
		const plainSize int64 = 285483787
		const want = 56

		naive := int((encryptedFileSize(plainSize) + config.UploadChunkSize - 1) / config.UploadChunkSize)
		if naive != 55 {
			t.Fatalf("expected naive calculation to be 55, got %d", naive)
		}

		if got := encryptedMultipartPartCount(plainSize, config.UploadChunkSize); got != want {
			t.Fatalf("expected %d parts, got %d", want, got)
		}
	})
}
