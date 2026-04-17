package auth

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("empty hash")
	}
	if !VerifyPassword(hash, "hunter2") {
		t.Error("correct password failed verification")
	}
	if VerifyPassword(hash, "wrong") {
		t.Error("wrong password verified as correct")
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	p, err := GenerateRandomPassword()
	if err != nil {
		t.Fatalf("GenerateRandomPassword: %v", err)
	}
	if len(p) < 32 {
		t.Errorf("password too short: %d chars", len(p))
	}
}
