package handler

func isValidUserRole(role string) bool {
	return role == "root" || role == "user"
}
