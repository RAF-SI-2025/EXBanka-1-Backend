package service

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPepperPassword_DiffersFromRaw(t *testing.T) {
	pepper := "test-pepper-secret"
	raw := "MyPassword12"
	peppered := PepperPassword(pepper, raw)
	if peppered == raw {
		t.Fatal("peppered password should differ from raw password")
	}
}

func TestPepperPassword_IsFixedLength64(t *testing.T) {
	// HMAC-SHA256 → 32 bytes → 64 hex chars — always, regardless of input length
	cases := []struct{ pepper, password string }{
		{"", ""},
		{"short", "pw"},
		{"a-32-char-pepper-secret-here-!!", "A1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{"very-long-pepper-that-exceeds-72-bytes-to-prove-no-truncation-issues-here-123456", "AShortPw12"},
	}
	for _, c := range cases {
		got := PepperPassword(c.pepper, c.password)
		if len(got) != 64 {
			t.Errorf("PepperPassword(%q, %q): expected 64 chars, got %d: %q", c.pepper, c.password, len(got), got)
		}
	}
}

func TestPepperPassword_DifferentPeppersProduceDifferentHashes(t *testing.T) {
	raw := "MyPassword12"
	h1 := PepperPassword("pepper1", raw)
	h2 := PepperPassword("pepper2", raw)
	if h1 == h2 {
		t.Fatal("different peppers should produce different hashes for the same password")
	}
}

func TestPepperPassword_SamePepperProducesSameHash(t *testing.T) {
	pepper := "stable-pepper"
	raw := "MyPassword12"
	h1 := PepperPassword(pepper, raw)
	h2 := PepperPassword(pepper, raw)
	if h1 != h2 {
		t.Fatal("same pepper+password should produce the same hash every time")
	}
}

func TestPepperPassword_BcryptIntegration(t *testing.T) {
	pepper := "server-secret"
	raw := "MyPassword12"
	peppered := PepperPassword(pepper, raw)

	hash, err := bcrypt.GenerateFromPassword([]byte(peppered), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash failed: %v", err)
	}

	// Verify with pepper succeeds
	if err := bcrypt.CompareHashAndPassword(hash, []byte(peppered)); err != nil {
		t.Fatal("expected verification to succeed with correct pepper")
	}

	// Verify without pepper (raw) fails
	if err := bcrypt.CompareHashAndPassword(hash, []byte(raw)); err == nil {
		t.Fatal("expected verification to fail without pepper")
	}

	// Verify with wrong pepper fails
	wrongPeppered := PepperPassword("wrong-pepper", raw)
	if err := bcrypt.CompareHashAndPassword(hash, []byte(wrongPeppered)); err == nil {
		t.Fatal("expected verification to fail with wrong pepper")
	}
}
