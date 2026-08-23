package handler

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/willvar/dofs"

	"domus/internal/middleware"
	"domus/internal/model"
)

const (
	dofsEventPollInterval = 500 * time.Millisecond
	dofsEventBatchSize    = 256
)

// StartDOFSEventRelay bridges namespace changes made through FUSE (including
// external DOFS mount writes) into the browser's directory invalidation
// channel. API-originated changes may produce a duplicate refresh, which is
// intentionally harmless and keeps DOFS as the sole event source of truth.
func (h *Handler) StartDOFSEventRelay(ctx context.Context) error {
	if h.DOFS == nil || h.FileSystem == nil || h.Hub == nil {
		return nil
	}
	cursors := make(map[string]int64)
	users, err := h.Repos.Users.List()
	if err != nil {
		return err
	}
	for index := range users {
		sequence, err := h.DOFS.Metadata.LatestEventSequence(ctx, users[index].ID)
		if err != nil {
			return err
		}
		cursors[users[index].ID] = sequence
	}
	go h.runDOFSEventRelay(ctx, cursors)
	return nil
}

func (h *Handler) runDOFSEventRelay(ctx context.Context, cursors map[string]int64) {
	ticker := time.NewTicker(dofsEventPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			users, err := h.Repos.Users.List()
			if err != nil {
				log.Printf("[dofs-events] list users: %v", err)
				continue
			}
			alive := make(map[string]struct{}, len(users))
			for index := range users {
				user := &users[index]
				alive[user.ID] = struct{}{}
				cursor, known := cursors[user.ID]
				if !known {
					cursor = 0
				}
				next, err := h.relayUserDOFSEvents(ctx, user, cursor)
				if err != nil {
					log.Printf("[dofs-events] relay user %s: %v", user.ID, err)
					continue
				}
				cursors[user.ID] = next
			}
			for userID := range cursors {
				if _, exists := alive[userID]; !exists {
					delete(cursors, userID)
				}
			}
		}
	}
}

func (h *Handler) relayUserDOFSEvents(ctx context.Context, user *model.User, cursor int64) (int64, error) {
	for {
		events, err := h.DOFS.Metadata.ListEvents(ctx, user.ID, cursor, dofsEventBatchSize)
		if err != nil {
			return cursor, err
		}
		for _, event := range events {
			h.publishDOFSEvent(user, event)
			cursor = event.Sequence
		}
		if len(events) < dofsEventBatchSize {
			return cursor, nil
		}
	}
}

func (h *Handler) publishDOFSEvent(user *model.User, event dofs.Event) {
	// Snapshot recipients before a remove revokes their capabilities.
	shares, _ := h.Repos.Shares.ListOwnedByUser(user.ID)
	if event.Kind == dofs.EventRemove {
		_ = h.Repos.Shares.DeleteByInode(user.ID, int64(event.Inode))
	} else if current, err := h.Repos.Files.GetByID(user.ID, int64(event.Inode)); err == nil && current != nil && !current.IsDir {
		_ = h.Repos.Shares.SyncByInode(
			user.ID, current.ID, current.Path, current.Name, current.Size, current.ContentType,
		)
	}
	parents := make(map[uint64]struct{}, 2)
	switch event.Kind {
	case dofs.EventRename:
		parents[event.Parent] = struct{}{}
		parents[event.OldParent] = struct{}{}
	case dofs.EventRemove:
		parents[event.OldParent] = struct{}{}
	default:
		parents[event.Parent] = struct{}{}
	}
	for inode := range parents {
		if inode == 0 {
			continue
		}
		parent, err := h.Repos.Files.GetByID(user.ID, int64(inode))
		if err != nil || !parent.IsDir {
			continue
		}
		if middleware.IsInternalStoragePath(parent.Path) {
			if strings.HasPrefix(parent.Path, trashStorageRootPath) {
				h.notifyTrashChanged(user.ID)
			}
			continue
		}
		h.Hub.PushDirChanged(user.ID, parent.Path, toAppPath(parent.Path, user.Username), "refresh")
	}

	// A stable inode keeps a share valid across rename. Notify recipients so
	// their virtual shared directory immediately reflects writes, moves and
	// removals performed through FUSE.
	for _, share := range shares {
		if share.FileInode == int64(event.Inode) {
			h.Hub.SendToUser(share.TargetUserID, map[string]any{
				"event": "dir.changed",
				"data":  map[string]any{"path": "__shared__/", "change_type": "refresh"},
			})
		}
	}
}
