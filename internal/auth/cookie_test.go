package auth

import "testing"

func TestSignCookie(t *testing.T) {
	signed := SignCookie("session123", "secret")
	if signed == "session123" {
		t.Fatal("signed cookie should include signature")
	}
	if len(signed) <= len("session123.") {
		t.Fatal("signed cookie too short")
	}
}

func TestVerifyCookie_Valid(t *testing.T) {
	secret := "my-secret"
	signed := SignCookie("session123", secret)
	id, err := VerifyCookie(signed, secret)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if id != "session123" {
		t.Fatalf("expected session123, got %s", id)
	}
}

func TestVerifyCookie_WrongSecret(t *testing.T) {
	signed := SignCookie("session123", "correct-secret")
	_, err := VerifyCookie(signed, "wrong-secret")
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestVerifyCookie_Malformed(t *testing.T) {
	_, err := VerifyCookie("no-dot-here", "secret")
	if err == nil {
		t.Fatal("expected error for malformed cookie")
	}
}
