package handler

import (
	"context"
	"log"
	"time"
)

const staleUploadAge = 24 * time.Hour

// StartUploadSweeper starts a background goroutine that periodically cleans up
// stale uploads (status="uploading" for > 24 hours). It aborts incomplete
// multipart uploads on OSS and removes orphaned file/task records.
func (h *Handler) StartUploadSweeper() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			h.sweepStaleUploads()
		}
	}()
}

func (h *Handler) sweepStaleUploads() {
	records, err := h.Repos.Files.GetStaleUploads(staleUploadAge)
	if err != nil {
		log.Printf("[sweeper] query stale uploads: %v", err)
		return
	}
	for _, r := range records {
		// Abort multipart upload if still in progress
		if r.OSSUploadID != "" {
			_ = h.Store.AbortMultipartUpload(r.StorageKey(), r.OSSUploadID)
		}
		if h.FileSystem != nil {
			_ = h.FileSystem.AbortDirectUpload(context.Background(), r.UserID, r.UploadID)
		}
		if r.TaskID != "" {
			_ = h.Repos.Tasks.UpdateStatus(r.TaskID, "cancelled")
		}
		log.Printf("[sweeper] cleaned stale upload: user=%s path=%s", r.UserID, r.Path)
	}
}
