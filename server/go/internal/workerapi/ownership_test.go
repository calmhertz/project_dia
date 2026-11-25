package workerapi_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
	"aagasa/internal/workerapi"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func (e *uploadEnv) foreignWorker(t *testing.T) domain.Worker {
	t.Helper()
	ctx := context.Background()
	station, err := e.repo.CreateStation(ctx, domain.Station{
		Name: "ELSEWHERE", Latitude: 12.9, Longitude: 77.5, AltitudeM: 900,
		Timezone: "UTC", ActiveRFBand: domain.BandVHF})
	if err != nil {
		t.Fatalf("create station: %v", err)
	}
	worker, err := e.repo.CreateWorker(ctx, domain.Worker{
		StationID: station.ID, Name: "worker-elsewhere", ConnectionState: domain.WorkerOffline})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	return worker
}

func (e *uploadEnv) passWithStatus(t *testing.T, aos time.Time, passStatus domain.PassStatus) domain.Pass {
	t.Helper()
	pass, err := e.repo.CreatePass(context.Background(), domain.Pass{
		StationID: e.stationID, SatelliteID: e.satelliteID, RequestedBy: e.requesterID,
		TLERecordID: e.tleID, SchedulingConfigID: e.configID,
		Status: passStatus, Visibility: domain.VisibilityPrivate,
		Band: domain.BandVHF, AOSAt: aos, LOSAt: aos.Add(10 * time.Minute),
		MaxElevationDegrees: 31,
		ReservedFrom:        aos.Add(-time.Minute), ReservedTo: aos.Add(11 * time.Minute),
		RecordingMode: domain.RecordRaw, RecordingPostRoll: 10 * time.Second})
	if err != nil {
		t.Fatalf("create pass: %v", err)
	}
	return pass
}

func (e *uploadEnv) statusOf(t *testing.T, passID uuid.UUID) domain.PassStatus {
	t.Helper()
	pass, err := e.repo.GetPassByID(context.Background(), passID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	return pass.Status
}

func TestUploadWithoutTheSecretIsRejected(t *testing.T) {
	env := newUploadEnv(t)

	clients := map[string]workerv1.WorkerServiceClient{
		"no secret":    env.dial(t),
		"wrong secret": env.dial(t, withStreamSecret("not-the-worker-secret-at-all-000")),
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			_, err := env.uploadWith(t, client, "baseband.ziq", []byte("content"), nil)
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("error = %v, want Unauthenticated", err)
			}
		})
	}

	count, err := env.repo.CountRecordings(context.Background(), env.passID)
	if err != nil {
		t.Fatalf("CountRecordings: %v", err)
	}
	if count != 0 {
		t.Errorf("an unauthenticated upload was stored: %d recordings", count)
	}
}

func TestUnaryCallsWithoutTheSecretAreRejected(t *testing.T) {
	env := newUploadEnv(t)

	_, err := env.dial(t).ReportExecutions(context.Background(), &workerv1.ReportExecutionsRequest{
		WorkerId: env.newWorker(t).ID.String(),
		Records:  []*workerv1.ExecutionRecord{{PassId: env.passID.String(), State: "completed"}}})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("error = %v, want Unauthenticated", err)
	}
	if got := env.statusOf(t, env.passID); got != domain.PassApproved {
		t.Errorf("status = %s, want approved", got)
	}
}

func TestUploadForAnotherStationsPassIsRefused(t *testing.T) {
	env := newUploadEnv(t)
	foreign := env.foreignWorker(t)

	_, err := env.upload(t, "baseband.ziq", []byte("content"), func(m *workerv1.RecordingMetadata) {
		m.WorkerId = foreign.ID.String()
		m.StationId = foreign.StationID.String()
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("foreign worker: error = %v, want PermissionDenied", err)
	}

	_, err = env.upload(t, "baseband.ziq", []byte("content"), func(m *workerv1.RecordingMetadata) {
		m.StationId = foreign.StationID.String()
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("claimed foreign station: error = %v, want PermissionDenied", err)
	}

	_, err = env.upload(t, "baseband.ziq", []byte("content"), func(m *workerv1.RecordingMetadata) {
		m.WorkerId = uuid.NewString()
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown worker: error = %v, want NotFound", err)
	}

	count, err := env.repo.CountRecordings(context.Background(), env.passID)
	if err != nil {
		t.Fatalf("CountRecordings: %v", err)
	}
	if count != 0 {
		t.Errorf("a refused upload was stored: %d recordings", count)
	}
}

func TestReportForAnotherStationsPassIsRefused(t *testing.T) {
	env := newUploadEnv(t)
	foreign := env.foreignWorker(t)

	response, err := env.client.ReportExecutions(context.Background(), &workerv1.ReportExecutionsRequest{
		WorkerId: foreign.ID.String(),
		Records:  []*workerv1.ExecutionRecord{{PassId: env.passID.String(), State: "failed"}}})
	if err != nil {
		t.Fatalf("ReportExecutions: %v (a foreign record is skipped, not fatal)", err)
	}
	if response.GetAccepted() != 0 {
		t.Errorf("accepted = %d, want 0 for another station's pass", response.GetAccepted())
	}
	if got := env.statusOf(t, env.passID); got != domain.PassApproved {
		t.Errorf("status = %s; a foreign worker changed the pass", got)
	}
}

func TestReportFromAnUnknownWorkerIsRefused(t *testing.T) {
	env := newUploadEnv(t)
	record := []*workerv1.ExecutionRecord{{PassId: env.passID.String(), State: "completed"}}

	_, err := env.client.ReportExecutions(context.Background(), &workerv1.ReportExecutionsRequest{
		WorkerId: uuid.NewString(), Records: record})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown worker: error = %v, want NotFound", err)
	}
	_, err = env.client.ReportExecutions(context.Background(), &workerv1.ReportExecutionsRequest{
		Records: record})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing worker: error = %v, want InvalidArgument", err)
	}
}

func TestHeartbeatMarksTheCurrentPassExecuting(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	encoded, err := proto.Marshal(&workerv1.PassPlan{PassId: env.passID.String(), Generation: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("encode plan: %v", err)
	}
	if _, err := env.repo.UpsertPassPlan(ctx, store.StoredPassPlan{
		PassID: env.passID, Generation: strings.Repeat("a", 64), PlanVersion: 1,
		Encoded: encoded, GeneratedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("UpsertPassPlan: %v", err)
	}

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), CurrentPassId: env.passID.String()}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got := env.statusOf(t, env.passID); got != domain.PassExecuting {
		t.Fatalf("status = %s, want executing", got)
	}

	plans, err := env.repo.ListPassPlansForStation(ctx, env.stationID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListPassPlansForStation: %v", err)
	}
	if len(plans) != 1 {
		t.Errorf("plans = %d; an executing pass must stay in the desired state", len(plans))
	}

	records, err := env.repo.ListAuditRecords(ctx, store.AuditFilter{
		EntityType: "pass", EntityID: env.passID.String(), Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) == 0 || records[0].Action != "pass.executing" {
		t.Errorf("the executing transition was not audited: %+v", records)
	}

	if _, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
		WorkerId: worker.ID.String(),
		Records:  []*workerv1.ExecutionRecord{{PassId: env.passID.String(), State: "completed"}}}); err != nil {
		t.Fatalf("ReportExecutions: %v", err)
	}
	if got := env.statusOf(t, env.passID); got != domain.PassCompleted {
		t.Errorf("status = %s, want completed", got)
	}
}

func TestHeartbeatIgnoresUnknownAndForeignPassIDs(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	foreign := env.foreignWorker(t)
	worker := env.newWorker(t)

	for name, request := range map[string]*workerv1.HeartbeatRequest{
		"foreign worker": {WorkerId: foreign.ID.String(), CurrentPassId: env.passID.String()},
		"unknown pass":   {WorkerId: worker.ID.String(), CurrentPassId: uuid.NewString()},
		"malformed":      {WorkerId: worker.ID.String(), CurrentPassId: "not-a-uuid"},
	} {
		if _, err := env.client.Heartbeat(ctx, request); err != nil {
			t.Errorf("%s: Heartbeat failed: %v", name, err)
		}
	}
	if got := env.statusOf(t, env.passID); got != domain.PassApproved {
		t.Errorf("status = %s, want approved", got)
	}

	rejected := env.passWithStatus(t, time.Now().UTC().Add(5*time.Hour), domain.PassRejected)
	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), CurrentPassId: rejected.ID.String()}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got := env.statusOf(t, rejected.ID); got != domain.PassRejected {
		t.Errorf("a rejected pass became %s", got)
	}
}

func TestSweeperMarksOverdueApprovedPassesMissed(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	now := time.Now().UTC()

	overdue := env.passWithStatus(t, now.Add(-3*time.Hour), domain.PassApproved)
	recent := env.passWithStatus(t, now.Add(-30*time.Minute), domain.PassApproved)
	executing := env.passWithStatus(t, now.Add(-2*time.Hour), domain.PassExecuting)
	abandoned := env.passWithStatus(t, now.Add(-8*time.Hour), domain.PassExecuting)

	marked, err := workerapi.SweepMissedPasses(ctx, env.repo, discardLogger(), now)
	if err != nil {
		t.Fatalf("SweepMissedPasses: %v", err)
	}
	if marked != 2 {
		t.Errorf("marked = %d, want 2", marked)
	}

	want := map[uuid.UUID]domain.PassStatus{
		overdue.ID:   domain.PassMissed,
		recent.ID:    domain.PassApproved,
		executing.ID: domain.PassExecuting,
		abandoned.ID: domain.PassMissed,
		env.passID:   domain.PassApproved,
	}
	for id, expected := range want {
		if got := env.statusOf(t, id); got != expected {
			t.Errorf("pass %s: status = %s, want %s", id, got, expected)
		}
	}

	records, err := env.repo.ListAuditRecords(ctx, store.AuditFilter{
		EntityType: "pass", EntityID: overdue.ID.String(), Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) != 1 || records[0].Action != "pass.missed" {
		t.Errorf("audit = %+v, want one pass.missed", records)
	}

	if again, err := workerapi.SweepMissedPasses(ctx, env.repo, discardLogger(), now); err != nil || again != 0 {
		t.Errorf("second sweep = %d, %v; want 0", again, err)
	}
}

func TestALateWorkerReportOverridesMissed(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	now := time.Now().UTC()
	worker := env.newWorker(t)

	completed := env.passWithStatus(t, now.Add(-3*time.Hour), domain.PassApproved)
	failed := env.passWithStatus(t, now.Add(-5*time.Hour), domain.PassApproved)
	rejected := env.passWithStatus(t, now.Add(-7*time.Hour), domain.PassRejected)
	if _, err := workerapi.SweepMissedPasses(ctx, env.repo, discardLogger(), now); err != nil {
		t.Fatalf("SweepMissedPasses: %v", err)
	}

	if _, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
		WorkerId: worker.ID.String(),
		Records: []*workerv1.ExecutionRecord{
			{PassId: completed.ID.String(), State: "completed"},
			{PassId: failed.ID.String(), State: "failed"},
			{PassId: rejected.ID.String(), State: "completed"},
		}}); err != nil {
		t.Fatalf("ReportExecutions: %v", err)
	}

	want := map[uuid.UUID]domain.PassStatus{
		completed.ID: domain.PassCompleted,
		failed.ID:    domain.PassFailed,
		rejected.ID:  domain.PassRejected,
	}
	for id, expected := range want {
		if got := env.statusOf(t, id); got != expected {
			t.Errorf("pass %s: status = %s, want %s", id, got, expected)
		}
	}

	if _, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
		WorkerId: worker.ID.String(),
		Records:  []*workerv1.ExecutionRecord{{PassId: completed.ID.String(), State: "failed"}}}); err != nil {
		t.Fatalf("ReportExecutions: %v", err)
	}
	if got := env.statusOf(t, completed.ID); got != domain.PassCompleted {
		t.Errorf("a completed pass was rewritten to %s", got)
	}
}

func newExecutionDatabase(t *testing.T) *mongo.Database {
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
	if err := store.EnsureMongoCollections(ctx, database); err != nil {
		t.Fatalf("ensure collections: %v", err)
	}
	return database
}

func TestReportedExecutionsAreWrittenToMongo(t *testing.T) {
	database := newExecutionDatabase(t)
	env := newUploadEnv(t, func(service *workerapi.Service) {
		service.WithExecutionLog(store.NewExecutionLog(database))
	})
	ctx := context.Background()
	worker := env.newWorker(t)
	recordedAt := time.Now().UTC().Truncate(time.Millisecond)

	request := &workerv1.ReportExecutionsRequest{
		WorkerId: worker.ID.String(),
		Records: []*workerv1.ExecutionRecord{{
			PassId: env.passID.String(), State: "completed", Detail: "decoded 42 frames",
			RecordedAt: timestamppb.New(recordedAt)}},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := env.client.ReportExecutions(ctx, request); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}

	collection := database.Collection(store.CollectionPassExecutions)
	count, err := collection.CountDocuments(ctx, bson.M{"pass_id": env.passID.String()})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("documents = %d, want 1 (a repeated report must not duplicate)", count)
	}

	var stored store.PassExecution
	if err := collection.FindOne(ctx, bson.M{"pass_id": env.passID.String()}).Decode(&stored); err != nil {
		t.Fatalf("find: %v", err)
	}
	if stored.WorkerID != worker.ID.String() || stored.StationID != env.stationID.String() {
		t.Errorf("identity = %s / %s", stored.WorkerID, stored.StationID)
	}
	if stored.Outcome != "completed" || stored.Details["detail"] != "decoded 42 frames" {
		t.Errorf("outcome = %q, details = %v", stored.Outcome, stored.Details)
	}
	if stored.EndedAt == nil || !stored.EndedAt.Equal(recordedAt) {
		t.Errorf("ended_at = %v, want %v", stored.EndedAt, recordedAt)
	}
	if stored.ReportedAt.IsZero() || stored.StartedAt.IsZero() {
		t.Error("started_at or reported_at is missing")
	}
}

func TestAMongoOutageDoesNotFailTheReport(t *testing.T) {
	client, err := mongo.Connect(context.Background(), options.Client().
		ApplyURI("mongodb://127.0.0.1:1").
		SetServerSelectionTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("configure mongo: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	env := newUploadEnv(t, func(service *workerapi.Service) {
		service.WithExecutionLog(store.NewExecutionLog(client.Database("unreachable")))
	})

	response, err := env.client.ReportExecutions(context.Background(), &workerv1.ReportExecutionsRequest{
		WorkerId: env.newWorker(t).ID.String(),
		Records:  []*workerv1.ExecutionRecord{{PassId: env.passID.String(), State: "completed"}}})
	if err != nil {
		t.Fatalf("ReportExecutions failed because of mongo: %v", err)
	}
	if response.GetAccepted() != 1 {
		t.Errorf("Accepted = %d, want 1", response.GetAccepted())
	}
	if got := env.statusOf(t, env.passID); got != domain.PassCompleted {
		t.Errorf("status = %s, want completed", got)
	}
}
