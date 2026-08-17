package handler

import (
	"context"
	"fmt"
	"log"
	"time"

	"domus/internal/model"
)

func (h *Handler) deleteUserCompletely(user *model.User) error {
	// Revoke authentication first so a new terminal or media request cannot
	// race the teardown after the existing WebSocket connections are closed.
	h.revokeUserSessions(user.ID)
	deleted := false
	defer func() {
		if !deleted {
			h.allowMediaJobsForUser(user.ID)
		}
	}()
	timeout := 2 * time.Minute
	if h.Config != nil && h.Config.Workspace.OperationTimeoutSeconds > 0 {
		timeout = time.Duration(h.Config.Workspace.OperationTimeoutSeconds) * time.Second
	}
	mediaContext, cancelMedia := context.WithTimeout(context.Background(), timeout)
	mediaErr := h.drainMediaJobsForUser(mediaContext, user.ID)
	cancelMedia()
	if mediaErr != nil {
		return fmt.Errorf("stop user media jobs: %w", mediaErr)
	}
	// Tear down the container and its plaintext DOFS mount while the database
	// identity still exists. This is fail-closed: deleting metadata first could
	// leave a live, unreachable container holding decrypted user data.
	// Give teardown its own operation budget. A media job may legitimately
	// spend most of its cancellation window flushing and deleting a partial
	// output; reusing that deadline would make an otherwise healthy workspace
	// removal fail before it starts.
	workspaceContext, cancelWorkspace := context.WithTimeout(context.Background(), timeout)
	_, err := h.Workspace.Remove(workspaceContext, user.ID)
	cancelWorkspace()
	if err != nil {
		return fmt.Errorf("stop user workspace: %w", err)
	}

	if err := h.Repos.Cleanup.DeleteUserAndRelatedData(user.ID); err != nil {
		return err
	}
	deleted = true

	if h.Store != nil {
		if err := h.Store.RecursiveDelete(user.Username+"/", nil); err != nil {
			log.Printf("[admin] user %s storage cleanup failed: %v", user.Username, err)
		}
		if err := h.Store.RecursiveDelete(model.DOFSObjectRoot(user.ID), nil); err != nil {
			log.Printf("[admin] user %s DOFS generation cleanup failed: %v", user.Username, err)
		}
	}

	return nil
}
