package handler

import (
	"context"
	"errors"
	"log"

	"github.com/willvar/dofs"

	"domus/internal/model"
)

func (h *Handler) deleteUserCompletely(user *model.User) error {
	// Revoke authentication before deleting application metadata and the DOFS
	// namespace so no authenticated request can race the teardown.
	h.revokeUserSessions(user.ID)

	if err := h.Repos.Cleanup.DeleteUserAndRelatedData(user.ID); err != nil {
		return err
	}

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
