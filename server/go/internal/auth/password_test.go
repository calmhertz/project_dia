package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	const password = "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword(password, hash); err != nil {
		t.Errorf("VerifyPassword: %v", err)
	}
}

// spec.md section 21 and global test property 20.
func TestHashIsArgon2id(t *testing.T) {
	hash, err := HashPassword("some-long-enough-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash %q is not argon2id", hash)
	}
	// The database CHECK constraint keys off exactly this prefix.
	if !strings.Contains(hash, "$v=19$") {
		t.Errorf("hash %q does not carry the argon2 version", hash)
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	const password = "some-long-enough-password"

	first, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Error("the same password produced an identical hash twice; salt is not random")
	}
	// Both must still verify.
	if err := VerifyPassword(password, first); err != nil {
		t.Errorf("first hash: %v", err)
	}
	if err := VerifyPassword(password, second); err != nil {
		t.Errorf("second hash: %v", err)
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	hash, err := HashPassword("the-real-password-value")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	for _, wrong := range []string{"", "the-real-password-valu", "the-real-password-value ", "THE-REAL-PASSWORD-VALUE"} {
		if err := VerifyPassword(wrong, hash); !errors.Is(err, ErrPasswordMismatch) {
			t.Errorf("VerifyPassword(%q) = %v, want ErrPasswordMismatch", wrong, err)
		}
	}
}

// A malformed hash must be distinguishable from a wrong password, so a corrupt
// record is never silently treated as a failed login.
func TestMalformedHashIsRejected(t *testing.T) {
	malformed := []string{
		"",
		"plaintext",
		"$2y$10$abcdefghijklmnopqrstuv",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA",
		"$argon2id$v=99$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!notbase64!!!$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$$",
	}

	for _, hash := range malformed {
		if err := VerifyPassword("anything", hash); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("VerifyPassword(hash=%q) = %v, want ErrInvalidHash", hash, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLength)); err != nil {
		t.Errorf("minimum length rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLength-1)); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short password error = %v, want ErrPasswordTooShort", err)
	}
	// The bootstrap password is public knowledge and must not survive setup.
	if err := ValidatePassword(BootstrapPassword); err == nil {
		t.Error("the bootstrap password was accepted as a new password")
	}
}
