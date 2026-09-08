// Package store opens and health-checks the Server's datastores.
//
// PostgreSQL is authoritative for scheduling and conflict prevention, MongoDB
// holds flexible operational documents, and Redis carries ephemeral state only
// (spec.md section 4.3). Schema and collections arrive in V2.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"aagasa/internal/config"
)

// Stores holds the Server's datastore handles.
type Stores struct {
	Postgres *pgxpool.Pool
	Redis    *redis.Client
	Mongo    *mongo.Client

	mongoDatabase string
}

// Open connects to every datastore and verifies each one responds.
// On failure it closes whatever was already opened.
func Open(ctx context.Context, cfg config.Config) (*Stores, error) {
	stores := &Stores{mongoDatabase: cfg.MongoDB}

	pool, err := pgxpool.New(ctx, cfg.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("configure postgres: %w", err)
	}
	stores.Postgres = pool
	if err := pool.Ping(ctx); err != nil {
		stores.Close(ctx)
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		stores.Close(ctx)
		return nil, fmt.Errorf("configure redis: %w", err)
	}
	stores.Redis = redis.NewClient(redisOptions)
	if err := stores.Redis.Ping(ctx).Err(); err != nil {
		stores.Close(ctx)
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURL))
	if err != nil {
		stores.Close(ctx)
		return nil, fmt.Errorf("configure mongodb: %w", err)
	}
	stores.Mongo = mongoClient
	if err := mongoClient.Ping(ctx, readpref.Primary()); err != nil {
		stores.Close(ctx)
		return nil, fmt.Errorf("connect mongodb: %w", err)
	}

	return stores, nil
}

// Database returns the configured MongoDB database handle.
func (s *Stores) Database() *mongo.Database {
	return s.Mongo.Database(s.mongoDatabase)
}

// PingPostgres reports whether PostgreSQL is reachable.
func (s *Stores) PingPostgres(ctx context.Context) error {
	if s.Postgres == nil {
		return fmt.Errorf("postgres not configured")
	}
	return s.Postgres.Ping(ctx)
}

// PingRedis reports whether Redis is reachable.
func (s *Stores) PingRedis(ctx context.Context) error {
	if s.Redis == nil {
		return fmt.Errorf("redis not configured")
	}
	return s.Redis.Ping(ctx).Err()
}

// PingMongo reports whether MongoDB is reachable.
func (s *Stores) PingMongo(ctx context.Context) error {
	if s.Mongo == nil {
		return fmt.Errorf("mongodb not configured")
	}
	return s.Mongo.Ping(ctx, readpref.Primary())
}

// Close releases every datastore handle. It is safe to call with partially
// opened stores.
func (s *Stores) Close(ctx context.Context) {
	if s.Postgres != nil {
		s.Postgres.Close()
	}
	if s.Redis != nil {
		_ = s.Redis.Close()
	}
	if s.Mongo != nil {
		_ = s.Mongo.Disconnect(ctx)
	}
}
