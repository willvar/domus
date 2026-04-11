package handler

import (
	"log"

	"zephyr/internal/model"
)

func (h *Handler) deleteUserCompletely(user *model.User) error {
	if err := h.Repos.Cleanup.DeleteUserAndRelatedData(user.ID); err != nil {
		return err
	}

	if h.Hub != nil {
		h.Hub.DisconnectUser(user.ID)
	}

	if h.Store != nil {
		if err := h.Store.RecursiveDelete(user.Username+"/", nil); err != nil {
			log.Printf("[admin] user %s storage cleanup failed: %v", user.Username, err)
		}
	}

	return nil
}
