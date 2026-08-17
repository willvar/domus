package workspace

import "testing"

func TestValidIdentityName(t *testing.T) {
	valid := []string{"alice", "Alice_01", "user.name", "user-name"}
	for _, value := range valid {
		if !ValidIdentityName(value) {
			t.Errorf("ValidIdentityName(%q) = false", value)
		}
	}
	invalid := []string{"", ".alice", "-alice", "alice@example.com", "alice/bob", "alice bob", "alice\nroot", string(make([]byte, 129))}
	for _, value := range invalid {
		if ValidIdentityName(value) {
			t.Errorf("ValidIdentityName(%q) = true", value)
		}
	}
}
