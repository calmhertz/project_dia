package store

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDB holds flexible pass-derived documents. Core transactional scheduling
// state stays in PostgreSQL (spec.md section 4.3).
const (
	CollectionPassExecutions  = "pass_executions"
	CollectionWorkerTelemetry = "worker_telemetry"
)

// TelemetryRetention bounds the operational telemetry collection so a
// long-running station cannot fill the disk with heartbeat documents.
const TelemetryRetention = 14 * 24 * time.Hour

// PassExecution is the operational record of one execution attempt. Its shape
// is intentionally loose; Details carries whatever the Worker reports.
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

// WorkerTelemetry is one operational sample from a Worker.
type WorkerTelemetry struct {
	WorkerID     string         `bson:"worker_id"`
	StationID    string         `bson:"station_id"`
	RecordedAt   time.Time      `bson:"recorded_at"`
	Measurements map[string]any `bson:"measurements,omitempty"`
}

// EnsureMongoDatabase connects and applies the collection bootstrap. It is the
// entrypoint used by the aagasa-migrate command.
func EnsureMongoDatabase(ctx context.Context, mongoURL, databaseName string) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURL))
	if err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	return EnsureMongoCollections(ctx, client.Database(databaseName))
}

// EnsureMongoCollections creates the operational collections and their indexes.
// It is safe to run repeatedly.
func EnsureMongoCollections(ctx context.Context, database *mongo.Database) error {
	executions := database.Collection(CollectionPassExecutions)
	// One execution record per pass attempt per worker; retries must not
	// silently produce a second logical record.
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
