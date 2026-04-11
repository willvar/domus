package handler

func (h *Handler) revokeUserSessions(userID string) {
	if h.Repos != nil && h.Repos.Sessions != nil {
		h.Repos.Sessions.DeleteByUserID(userID)
	}
	if h.Hub != nil {
		h.Hub.PushSessionExpired(userID)
		h.Hub.DisconnectUser(userID)
	}
}
