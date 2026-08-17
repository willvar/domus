package handler

import (
	"math"
	"testing"

	"domus/config"
	"domus/internal/store"
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

func TestAuthoritativeCompleteParts(t *testing.T) {
	t.Run("accepts contiguous OSS parts with the expected total size", func(t *testing.T) {
		parts, ok := authoritativeCompleteParts([]store.PartInfo{
			{PartNumber: 1, Size: 5, ETag: "etag-one"},
			{PartNumber: 2, Size: 7, ETag: "etag-two"},
		}, 2, 12)
		if !ok {
			t.Fatal("expected parts to be accepted")
		}
		if len(parts) != 2 || parts[0].PartNumber != 1 || parts[0].ETag != "etag-one" || parts[1].PartNumber != 2 {
			t.Fatalf("unexpected completion parts: %+v", parts)
		}
	})

	for _, tc := range []struct {
		name          string
		parts         []store.PartInfo
		expectedCount int
		expectedSize  int64
	}{
		{name: "missing part", parts: []store.PartInfo{{PartNumber: 1, Size: 5, ETag: "etag"}}, expectedCount: 2, expectedSize: 5},
		{name: "non-contiguous part number", parts: []store.PartInfo{{PartNumber: 2, Size: 5, ETag: "etag"}}, expectedCount: 1, expectedSize: 5},
		{name: "missing authoritative etag", parts: []store.PartInfo{{PartNumber: 1, Size: 5}}, expectedCount: 1, expectedSize: 5},
		{name: "wrong total size", parts: []store.PartInfo{{PartNumber: 1, Size: 4, ETag: "etag"}}, expectedCount: 1, expectedSize: 5},
		{name: "empty part", parts: []store.PartInfo{{PartNumber: 1, Size: 0, ETag: "etag"}}, expectedCount: 1, expectedSize: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := authoritativeCompleteParts(tc.parts, tc.expectedCount, tc.expectedSize); ok {
				t.Fatal("expected parts to be rejected")
			}
		})
	}
}
