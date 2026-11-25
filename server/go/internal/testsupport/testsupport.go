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

func PostgresURL(t *testing.T) string {
	t.Helper()
	return envOrSkip(t, "AAGASA_TEST_POSTGRES_URL")
}

func RedisURL(t *testing.T) string {
	t.Helper()
	return envOrSkip(t, "AAGASA_TEST_REDIS_URL")
}

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
		_, _ = conn.Exec(cleanupCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, name)
		if _, err := conn.Exec(cleanupCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, name)); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
	})

	return replaceDatabaseName(baseURL, name)
}

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
