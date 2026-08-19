package handler

import (
	"context"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
)

const dofsReclaimInterval = 30 * time.Second

// StartDOFSReclaimer retries durable DOFS tombstones independently of FUSE and
// request lifetimes. A one-shot pass runs immediately so crash leftovers do
// not wait for the first ticker interval.
func (h *Handler) StartDOFSReclaimer(ctx context.Context) {
	if h == nil || h.DOFS == nil {
		return
	}
	go func() {
		h.reclaimAllNamespaces(ctx)
		ticker := time.NewTicker(dofsReclaimInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.reclaimAllNamespaces(ctx)
			}
		}
	}()
}

func (h *Handler) reclaimAllNamespaces(ctx context.Context) {
	if h == nil || h.DOFS == nil || h.DOFS.Metadata == nil || h.Repos == nil || h.Repos.Users == nil {
		return
	}
	namespaces, err := h.DOFS.Metadata.ListNamespaces(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[dofs-reclaim] list namespaces: %v", err)
		}
		return
	}
	for _, namespace := range namespaces {
		if _, userErr := h.Repos.Users.GetByID(namespace.ID); errors.Is(userErr, gorm.ErrRecordNotFound) {
			passContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			deleteErr := h.DOFS.DeleteNamespace(passContext, namespace.ID)
			cancel()
			if deleteErr != nil {
				if ctx.Err() == nil {
					log.Printf("[dofs-reclaim] orphan namespace %s: %v", namespace.ID, deleteErr)
				}
			} else {
				log.Printf("[dofs-reclaim] deleted orphan namespace %s", namespace.ID)
			}
			continue
		} else if userErr != nil {
			if ctx.Err() == nil {
				log.Printf("[dofs-reclaim] resolve namespace owner %s: %v", namespace.ID, userErr)
			}
			continue
		}
		passContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := h.DOFS.ReclaimNamespace(passContext, namespace.ID)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[dofs-reclaim] namespace %s: %v", namespace.ID, err)
			}
			continue
		}
		if result.Purged > 0 {
			log.Printf("[dofs-reclaim] namespace %s: purged %d inode(s)", namespace.ID, result.Purged)
		}
	}
}

func (h *Handler) reclaimNamespaceBestEffort(userID string) {
	if h == nil || h.DOFS == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.DOFS.ReclaimNamespace(ctx, userID); err != nil {
		log.Printf("[dofs-reclaim] immediate namespace %s pass failed; queued for retry: %v", userID, err)
	}
}
