package store

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	CollectionPassExecutions  = "pass_executions"
	CollectionWorkerTelemetry = "worker_telemetry"
)

const TelemetryRetention = 14 * 24 * time.Hour

type PassExecution struct {
	PassID      string         `bson:"pass_id"`
	WorkerID    string         `bson:"worker_id"`
	StationID   string         `bson:"station_id"`
	SatelliteID string         `bson:"satellite_id"`
	StartedAt   time.Time      `bson:"started_at"`
	EndedAt     *time.Time     `bson:"ended_at,omitempty"`
	Outcome     string         `bson:"outcome"`
	Details     map[string]any `bson:"details,omitempty"`
}

type WorkerTelemetry struct {
	WorkerID     string         `bson:"worker_id"`
	StationID    string         `bson:"station_id"`
	RecordedAt   time.Time      `bson:"recorded_at"`
	Measurements map[string]any `bson:"measurements,omitempty"`
}

func EnsureMongoDatabase(ctx context.Context, mongoURL, databaseName string) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURL))
	if err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	return EnsureMongoCollections(ctx, client.Database(databaseName))
}

func EnsureMongoCollections(ctx context.Context, database *mongo.Database) error {
	executions := database.Collection(CollectionPassExecutions)
	_, err := executions.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "pass_id", Value: 1}, {Key: "started_at", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("pass_execution_attempt"),
		},
		{
			Keys:    bson.D{{Key: "station_id", Value: 1}, {Key: "started_at", Value: -1}},
			Options: options.Index().SetName("station_recent_executions"),
		},
	})
	if err != nil {
		return fmt.Errorf("create %s indexes: %w", CollectionPassExecutions, err)
	}

	telemetry := database.Collection(CollectionWorkerTelemetry)
	_, err = telemetry.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "worker_id", Value: 1}, {Key: "recorded_at", Value: -1}},
			Options: options.Index().SetName("worker_recent_telemetry"),
		},
		{
			Keys: bson.D{{Key: "recorded_at", Value: 1}},
			Options: options.Index().
				SetName("telemetry_expiry").
				SetExpireAfterSeconds(int32(TelemetryRetention.Seconds())),
		},
	})
	if err != nil {
		return fmt.Errorf("create %s indexes: %w", CollectionWorkerTelemetry, err)
	}

	return nil
}
