// Package auth handles credentials, sessions and role checks.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. spec.md section 21 mandates Argon2id; these values match
// the working reference implementation and OWASP's second recommended profile.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// MinPasswordLength is the shortest password the Server accepts.
const MinPasswordLength = 12

// BootstrapUsername and BootstrapPassword are the first-run Root credentials
// required by spec.md section 17.1. They stop working the moment Root changes
// the password, and the Server refuses normal operation until then.
const (
	BootstrapUsername = "root"
	BootstrapPassword = "toor"
)

var (
	// ErrPasswordMismatch means the password did not match the hash.
	ErrPasswordMismatch = errors.New("password does not match")
	// ErrInvalidHash means the stored hash is not a usable Argon2id hash.
	ErrInvalidHash = errors.New("invalid password hash")
	// ErrPasswordTooShort means the password fails the length policy.
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	// ErrPasswordIsBootstrap means Root tried to keep the well-known password.
	ErrPasswordIsBootstrap = errors.New("the bootstrap password cannot be reused")
)

// ValidatePassword applies the password policy.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	// The bootstrap credential is public knowledge; it must not survive setup.
	if password == BootstrapPassword {
		return ErrPasswordIsBootstrap
	}
	return nil
}

// HashPassword returns a PHC-encoded Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	threads := argonThreads
	// Never ask for more parallelism than the machine has, or verification on a
	// smaller host could behave differently.
	if available := runtime.NumCPU(); available < int(threads) {
		threads = uint8(available)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, threads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a stored hash. It reports
// ErrPasswordMismatch for a wrong password and ErrInvalidHash for a hash it
// cannot parse, so a corrupt record is never mistaken for a bad password.
func VerifyPassword(password, encodedHash string) error {
	memory, time, threads, salt, want, err := decodeHash(encodedHash)
	if err != nil {
		return err
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

func decodeHash(encodedHash string) (memory, time uint32, threads uint8, salt, key []byte, err error) {
	parts := strings.Split(encodedHash, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if memory == 0 || time == 0 || threads == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}

	salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}

	return memory, time, threads, salt, key, nil
}
