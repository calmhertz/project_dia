// Package testsupport provides database fixtures for integration tests.
//
// It is imported only from _test files, so it does not ship in any binary.
package testsupport

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// PostgresURL returns the configured test database URL, skipping if unset.
func PostgresURL(t *testing.T) string {
	t.Helper()
	return envOrSkip(t, "AAGASA_TEST_POSTGRES_URL")
}

// RedisURL returns the configured test Redis URL, skipping if unset.
func RedisURL(t *testing.T) string {
	t.Helper()
	return envOrSkip(t, "AAGASA_TEST_REDIS_URL")
}

// MongoURL returns the configured test MongoDB URL, skipping if unset.
func MongoURL(t *testing.T) string {
	t.Helper()
	return envOrSkip(t, "AAGASA_TEST_MONGO_URL")
}

func envOrSkip(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s not set", name)
	}
	return value
}

// NewDatabase creates a throwaway database and returns its URL.
//
// Each test gets its own database so packages can run in parallel without
// resetting each other's schema.
func NewDatabase(t *testing.T) string {
	t.Helper()
	baseURL := PostgresURL(t)
	ctx := context.Background()

	name := "aagasa_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect for database creation: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create database %s: %v", name, err)
	}
	_ = admin.Close(ctx)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		conn, err := pgx.Connect(cleanupCtx, baseURL)
		if err != nil {
			t.Logf("cleanup connect failed: %v", err)
			return
		}
		defer func() { _ = conn.Close(cleanupCtx) }()
		// Terminate stragglers so the drop cannot be blocked by a live session.
		_, _ = conn.Exec(cleanupCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, name)
		if _, err := conn.Exec(cleanupCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, name)); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
	})

	return replaceDatabaseName(baseURL, name)
}

// NewRedis returns a Redis client for tests.
//
// It deliberately does not flush: session keys are hashes of 256-bit random
// tokens, so tests cannot collide, and flushing a shared Redis would destroy
// the sessions of tests running concurrently in another package. Leftover
// keys expire on their own TTL.
func NewRedis(t *testing.T) *redis.Client {
	t.Helper()
	options, err := redis.ParseURL(RedisURL(t))
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// replaceDatabaseName swaps the database path segment of a PostgreSQL URL.
func replaceDatabaseName(url, name string) string {
	base, query, hasQuery := strings.Cut(url, "?")
	slash := strings.LastIndex(base, "/")
	if slash < 0 {
		return url
	}
	replaced := base[:slash+1] + name
	if hasQuery {
		return replaced + "?" + query
	}
	return replaced
}
