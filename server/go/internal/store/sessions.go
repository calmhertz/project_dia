package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Sessions are ephemeral, so Redis owns them (spec.md section 4.3). Durable
// business state never lives here.
const sessionKeyPrefix = "aagasa:session:"

// tokenBytes gives a 256-bit session token.
const tokenBytes = 32

// ErrSessionNotFound is returned for a missing or expired session.
var ErrSessionNotFound = errors.New("session not found")

// Session is the state carried by a session token.
type Session struct {
	UserID    uuid.UUID `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionStore issues and resolves session tokens.
//
// V2 provides the primitives only; login, expiry policy and role checks are V3.
type SessionStore struct {
	client *redis.Client
}

// NewSessionStore builds a session store over an open Redis client.
func NewSessionStore(client *redis.Client) *SessionStore {
	return &SessionStore{client: client}
}

// NewToken returns a fresh random session token.
func NewToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// key hashes the token so a Redis dump does not reveal usable credentials.
func key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return sessionKeyPrefix + hex.EncodeToString(sum[:])
}

// Create stores a session under the token and returns when it will expire.
func (s *SessionStore) Create(ctx context.Context, token string, session Session, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("session ttl must be positive")
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := s.client.Set(ctx, key(token), payload, ttl).Err(); err != nil {
		return fmt.Errorf("store session: %w", err)
	}
	return nil
}

// Get resolves a token to its session.
func (s *SessionStore) Get(ctx context.Context, token string) (Session, error) {
	payload, err := s.client.Get(ctx, key(token)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(payload, &session); err != nil {
		return Session{}, fmt.Errorf("decode session: %w", err)
	}
	return session, nil
}

// Delete revokes a session. Revoking an unknown token is not an error.
func (s *SessionStore) Delete(ctx context.Context, token string) error {
	if err := s.client.Del(ctx, key(token)).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
