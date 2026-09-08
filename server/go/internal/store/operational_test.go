package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"aagasa/internal/store"
	"aagasa/internal/testsupport"
)

func newTestMongo(t *testing.T) *mongo.Database {
	t.Helper()
	url := testsupport.MongoURL(t)
	ctx := context.Background()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	database := client.Database("aagasa_test_" + uuid.NewString()[:8])
	t.Cleanup(func() {
		_ = database.Drop(ctx)
		_ = client.Disconnect(ctx)
	})
	return database
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	return testsupport.NewRedis(t)
}

func TestEnsureMongoCollectionsIsRepeatable(t *testing.T) {
	ctx := context.Background()
	database := newTestMongo(t)

	// Bootstrap must be safe to run on every deployment, not just the first.
	for attempt := 0; attempt < 2; attempt++ {
		if err := store.EnsureMongoCollections(ctx, database); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}

	for _, collection := range []string{store.CollectionPassExecutions, store.CollectionWorkerTelemetry} {
		cursor, err := database.Collection(collection).Indexes().List(ctx)
		if err != nil {
			t.Fatalf("list %s indexes: %v", collection, err)
		}
		var indexes []bson.M
		if err := cursor.All(ctx, &indexes); err != nil {
			t.Fatalf("read %s indexes: %v", collection, err)
		}
		// The default _id index plus the two declared for each collection.
		if len(indexes) != 3 {
			t.Errorf("%s has %d indexes, want 3", collection, len(indexes))
		}
	}
}

// Retrying a pass execution write must not create a second logical record.
func TestPassExecutionAttemptIsUnique(t *testing.T) {
	ctx := context.Background()
	database := newTestMongo(t)
	if err := store.EnsureMongoCollections(ctx, database); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	execution := store.PassExecution{
		PassID: "pass-1", WorkerID: "worker-1", StationID: "station-1",
		SatelliteID: "sat-1", StartedAt: time.Now().UTC().Truncate(time.Millisecond),
		Outcome: "completed", Details: map[string]any{"pipeline": "noaa_apt"},
	}

	collection := database.Collection(store.CollectionPassExecutions)
	if _, err := collection.InsertOne(ctx, execution); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := collection.InsertOne(ctx, execution); !mongo.IsDuplicateKeyError(err) {
		t.Errorf("duplicate insert error = %v, want duplicate key", err)
	}

	count, err := collection.CountDocuments(ctx, bson.M{"pass_id": "pass-1"})
	if err != nil || count != 1 {
		t.Errorf("count = %d, %v; want 1", count, err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	sessions := store.NewSessionStore(newTestRedis(t))

	token, err := store.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	userID := uuid.New()

	if err := sessions.Create(ctx, token, store.Session{
		UserID: userID, Role: "user", CreatedAt: time.Now().UTC(),
	}, time.Minute); err != nil {
		t.Fatalf("Create: %v", err)
	}

	session, err := sessions.Get(ctx, token)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if session.UserID != userID {
		t.Errorf("UserID = %v, want %v", session.UserID, userID)
	}

	if err := sessions.Delete(ctx, token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := sessions.Get(ctx, token); !errors.Is(err, store.ErrSessionNotFound) {
		t.Errorf("after delete error = %v, want ErrSessionNotFound", err)
	}
}

func TestUnknownSessionTokenIsRejected(t *testing.T) {
	ctx := context.Background()
	sessions := store.NewSessionStore(newTestRedis(t))

	if _, err := sessions.Get(ctx, "not-a-real-token"); !errors.Is(err, store.ErrSessionNotFound) {
		t.Errorf("error = %v, want ErrSessionNotFound", err)
	}
}

func TestSessionExpires(t *testing.T) {
	ctx := context.Background()
	sessions := store.NewSessionStore(newTestRedis(t))

	token, err := store.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := sessions.Create(ctx, token, store.Session{UserID: uuid.New()}, time.Second); err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	if _, err := sessions.Get(ctx, token); !errors.Is(err, store.ErrSessionNotFound) {
		t.Errorf("expired session error = %v, want ErrSessionNotFound", err)
	}
}

// The raw token must not appear as a Redis key: a dump of the keyspace should
// not hand an attacker usable credentials.
func TestSessionTokenIsNotStoredInTheKey(t *testing.T) {
	ctx := context.Background()
	client := newTestRedis(t)
	sessions := store.NewSessionStore(client)

	token, err := store.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := sessions.Create(ctx, token, store.Session{UserID: uuid.New()}, time.Minute); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Delete(ctx, token) })

	keys, err := client.Keys(ctx, "aagasa:session:*").Result()
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	for _, key := range keys {
		if key == "aagasa:session:"+token {
			t.Fatal("session key contains the raw token")
		}
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := store.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if seen[token] {
			t.Fatal("NewToken produced a duplicate")
		}
		seen[token] = true
	}
}
