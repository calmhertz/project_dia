package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const DefaultLoginAttemptPrefix = "aagasa:login:"

const (
	LoginAttemptWindow   = 15 * time.Minute
	MaxFailuresPerUser   = 5
	MaxFailuresPerClient = 20
)

// LoginAttempts counts failed logins per username and per client address in
// Redis, so the limit holds across restarts and server processes. Each counter
// is a fixed window that starts at its first failure.
type LoginAttempts struct {
	client *redis.Client
	prefix string
}

func NewLoginAttempts(client *redis.Client, prefix string) *LoginAttempts {
	if prefix == "" {
		prefix = DefaultLoginAttemptPrefix
	}
	return &LoginAttempts{client: client, prefix: prefix}
}

func (l *LoginAttempts) userKey(username string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return l.prefix + "user:" + hex.EncodeToString(sum[:])
}

func (l *LoginAttempts) clientKey(address string) string {
	return l.prefix + "client:" + address
}

// Blocked reports whether either counter has reached its limit and, if so,
// how long until the longer-lived of the exhausted windows expires.
func (l *LoginAttempts) Blocked(ctx context.Context, username, address string) (bool, time.Duration, error) {
	checks := []struct {
		key   string
		limit int64
	}{
		{l.userKey(username), MaxFailuresPerUser},
		{l.clientKey(address), MaxFailuresPerClient},
	}

	var retryAfter time.Duration
	blocked := false
	for _, check := range checks {
		count, err := l.client.Get(ctx, check.key).Int64()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return false, 0, fmt.Errorf("read login attempts: %w", err)
		}
		if count < check.limit {
			continue
		}
		blocked = true
		ttl, err := l.client.TTL(ctx, check.key).Result()
		if err != nil {
			return false, 0, fmt.Errorf("read login attempt window: %w", err)
		}
		if ttl > retryAfter {
			retryAfter = ttl
		}
	}
	return blocked, retryAfter, nil
}

func (l *LoginAttempts) RecordFailure(ctx context.Context, username, address string) error {
	_, err := l.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, key := range []string{l.userKey(username), l.clientKey(address)} {
			pipe.Incr(ctx, key)
			pipe.ExpireNX(ctx, key, LoginAttemptWindow)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	return nil
}

// Succeeded clears the username counter. The client counter is left alone so
// one valid account cannot be used to reset guessing against others.
func (l *LoginAttempts) Succeeded(ctx context.Context, username string) error {
	if err := l.client.Del(ctx, l.userKey(username)).Err(); err != nil {
		return fmt.Errorf("clear login failures: %w", err)
	}
	return nil
}
