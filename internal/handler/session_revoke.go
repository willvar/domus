package handler

func (h *Handler) revokeUserSessions(userID string) {
	if h.Sessions != nil {
		h.Sessions.DeleteByUserID(userID)
	}
	if h.Hub != nil {
		h.Hub.PushSessionExpired(userID)
		h.Hub.DisconnectUser(userID)
	}
}
