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

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

const MinPasswordLength = 12

const (
	BootstrapUsername = "root"
	BootstrapPassword = "toor"
)

var (
	ErrPasswordMismatch = errors.New("password does not match")
	ErrInvalidHash = errors.New("invalid password hash")
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	ErrPasswordIsBootstrap = errors.New("the bootstrap password cannot be reused")
)

func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if password == BootstrapPassword {
		return ErrPasswordIsBootstrap
	}
	return nil
}

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	threads := argonThreads
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
