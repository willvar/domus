package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/willvar/dofs"

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

	if h.DOFS == nil {
		log.Printf("[admin] user %s DOFS namespace cleanup skipped: runtime unavailable", user.Username)
		return nil
	}
	if err := h.DOFS.DeleteNamespace(context.Background(), user.ID); err != nil && !errors.Is(err, dofs.ErrNotFound) {
		// The user identity and all access grants are already gone. A failure
		// here can leave only unreachable encrypted metadata/objects; report it
		// for operator cleanup without reviving the deleted account.
		log.Printf("[admin] user %s DOFS namespace cleanup failed: %v", user.Username, err)
	}

	return nil
}
