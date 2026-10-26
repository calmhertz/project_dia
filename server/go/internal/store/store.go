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

type Stores struct {
	Postgres *pgxpool.Pool
	Redis    *redis.Client
	Mongo    *mongo.Client

	mongoDatabase string
}

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

func (s *Stores) Database() *mongo.Database {
	return s.Mongo.Database(s.mongoDatabase)
}

func (s *Stores) PingPostgres(ctx context.Context) error {
	if s.Postgres == nil {
		return fmt.Errorf("postgres not configured")
	}
	return s.Postgres.Ping(ctx)
}

func (s *Stores) PingRedis(ctx context.Context) error {
	if s.Redis == nil {
		return fmt.Errorf("redis not configured")
	}
	return s.Redis.Ping(ctx).Err()
}

func (s *Stores) PingMongo(ctx context.Context) error {
	if s.Mongo == nil {
		return fmt.Errorf("mongodb not configured")
	}
	return s.Mongo.Ping(ctx, readpref.Primary())
}

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
