package handler

import "zephyr/internal/model"

func ensureNotDemotingLastRoot(user *model.User, newRole string) (blockedCode string, err error) {
	if user.Role != "root" || newRole == "root" {
		return "", nil
	}
	rootCount, err := model.CountUsersByRole("root")
	if err != nil {
		return "", err
	}
	if rootCount <= 1 {
		return "cannot_demote_last_root", nil
	}
	return "", nil
}

func ensureNotDeletingLastRoot(user *model.User) (blockedCode string, err error) {
	if user.Role != "root" {
		return "", nil
	}
	rootCount, err := model.CountUsersByRole("root")
	if err != nil {
		return "", err
	}
	if rootCount <= 1 {
		return "cannot_delete_last_root", nil
	}
	return "", nil
}
