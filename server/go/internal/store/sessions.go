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

const sessionKeyPrefix = "aagasa:session:"

const tokenBytes = 32

var ErrSessionNotFound = errors.New("session not found")

type Session struct {
	UserID    uuid.UUID `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type SessionStore struct {
	client *redis.Client
}

func NewSessionStore(client *redis.Client) *SessionStore {
	return &SessionStore{client: client}
}

func NewToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return sessionKeyPrefix + hex.EncodeToString(sum[:])
}

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

func (s *SessionStore) Delete(ctx context.Context, token string) error {
	if err := s.client.Del(ctx, key(token)).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
