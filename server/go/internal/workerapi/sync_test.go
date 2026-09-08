package workerapi_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/store"

	"aagasa/internal/workerapi"
)

// Identity ----------------------------------------------------------------

// The Worker is configured with a name; the Server owns the identifiers.
func TestRegisterResolvesIdentityFromTheName(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()

	worker, err := env.repo.CreateWorker(ctx, domain.Worker{
		StationID: env.stationID, Name: "worker-1", ConnectionState: domain.WorkerOffline})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	response, err := env.client.Register(ctx, &workerv1.RegisterRequest{
		WorkerName: "worker-1", WorkerVersion: "0.1.0"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if response.GetWorkerId() != worker.ID.String() {
		t.Errorf("WorkerId = %s, want %s", response.GetWorkerId(), worker.ID)
	}
	if response.GetStationId() != env.stationID.String() {
		t.Error("the wrong station was returned")
	}
	if response.GetStationName() != "SJCIT" {
		t.Errorf("StationName = %q", response.GetStationName())
	}
}

func TestRegisteringAnUnknownNameIsRefused(t *testing.T) {
	env := newUploadEnv(t)

	_, err := env.client.Register(context.Background(),
		&workerv1.RegisterRequest{WorkerName: "not-configured"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("error = %v, want NotFound", err)
	}
}

func TestRegisterRequiresAName(t *testing.T) {
	env := newUploadEnv(t)

	_, err := env.client.Register(context.Background(), &workerv1.RegisterRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("error = %v, want InvalidArgument", err)
	}
}

// Heartbeat ---------------------------------------------------------------

func (e *uploadEnv) newWorker(t *testing.T) domain.Worker {
	t.Helper()
	worker, err := e.repo.CreateWorker(context.Background(), domain.Worker{
		StationID: e.stationID, Name: "worker-1", ConnectionState: domain.WorkerOffline})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	return worker
}

func TestHeartbeatBringsAWorkerOnline(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	response, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), WorkerVersion: "0.1.0",
		PendingUploads: 3, PendingReports: 2})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if response.GetServerTimeUnixMs() == 0 {
		t.Error("no server time returned")
	}

	updated, err := env.repo.GetWorkerByID(ctx, worker.ID)
	if err != nil {
		t.Fatalf("GetWorkerByID: %v", err)
	}
	if updated.ConnectionState != domain.WorkerOnline {
		t.Errorf("state = %s, want online", updated.ConnectionState)
	}
	if updated.LastSeenAt == nil {
		t.Error("last_seen_at was not recorded")
	}
	if updated.WorkerVersion != "0.1.0" {
		t.Errorf("version = %q", updated.WorkerVersion)
	}
	// The backlog is visible to an operator during an outage.
	if updated.PendingUploads != 3 || updated.PendingReports != 2 {
		t.Errorf("backlog = %d uploads, %d reports", updated.PendingUploads, updated.PendingReports)
	}
}

// A Worker holding the current generation should not be told to re-sync.
func TestHeartbeatReportsWhetherStateIsCurrent(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	// With no plans, the empty set still has a stable generation.
	first, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), StateGeneration: ""})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if first.GetStateIsCurrent() {
		t.Error("an empty generation was reported as current")
	}

	// Echoing back what the Server said makes the Worker current.
	second, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), StateGeneration: first.GetDesiredGeneration()})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !second.GetStateIsCurrent() {
		t.Error("the matching generation was not reported as current")
	}

	updated, err := env.repo.GetWorkerByID(ctx, worker.ID)
	if err != nil {
		t.Fatalf("GetWorkerByID: %v", err)
	}
	// Only reaching the current state counts as a completed sync.
	if updated.LastSyncAt == nil {
		t.Error("last_sync_at was not recorded once current")
	}
	if updated.SyncedGeneration != first.GetDesiredGeneration() {
		t.Error("the synced generation was not recorded")
	}
}

func TestHeartbeatFromAnUnknownWorkerIsRefused(t *testing.T) {
	env := newUploadEnv(t)

	_, err := env.client.Heartbeat(context.Background(), &workerv1.HeartbeatRequest{
		WorkerId: "00000000-0000-0000-0000-000000000000"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("error = %v, want NotFound", err)
	}

	_, err = env.client.Heartbeat(context.Background(), &workerv1.HeartbeatRequest{
		WorkerId: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("error = %v, want InvalidArgument", err)
	}
}

// Going offline -----------------------------------------------------------

// spec.md section 6.3: a station that stops reporting must be shown offline.
func TestAWorkerThatStopsReportingGoesOffline(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String()}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	// Nothing goes offline while it is still reporting.
	names, err := env.repo.MarkStaleWorkersOffline(ctx, time.Now().UTC().Add(-workerapi.StaleAfter))
	if err != nil {
		t.Fatalf("MarkStaleWorkersOffline: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("a live worker was marked offline: %v", names)
	}

	// Once the last heartbeat is older than the window, it does.
	names, err = env.repo.MarkStaleWorkersOffline(ctx, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("MarkStaleWorkersOffline: %v", err)
	}
	if len(names) != 1 || names[0] != "worker-1" {
		t.Fatalf("marked offline = %v, want [worker-1]", names)
	}

	updated, err := env.repo.GetWorkerByID(ctx, worker.ID)
	if err != nil {
		t.Fatalf("GetWorkerByID: %v", err)
	}
	if updated.ConnectionState != domain.WorkerOffline {
		t.Errorf("state = %s, want offline", updated.ConnectionState)
	}
}

// The transition is reported once, so the log does not repeat every sweep.
func TestGoingOfflineIsReportedOnce(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String()}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	cutoff := time.Now().UTC().Add(time.Minute)
	first, _ := env.repo.MarkStaleWorkersOffline(ctx, cutoff)
	second, _ := env.repo.MarkStaleWorkersOffline(ctx, cutoff)

	if len(first) != 1 {
		t.Errorf("first sweep = %v, want one transition", first)
	}
	if len(second) != 0 {
		t.Errorf("second sweep = %v, want no repeat", second)
	}
}

// A Worker that comes back must be shown online again.
func TestAReturningWorkerComesBackOnline(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String()}); err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if _, err := env.repo.MarkStaleWorkersOffline(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("mark offline: %v", err)
	}

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String()}); err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}

	updated, err := env.repo.GetWorkerByID(ctx, worker.ID)
	if err != nil {
		t.Fatalf("GetWorkerByID: %v", err)
	}
	if updated.ConnectionState != domain.WorkerOnline {
		t.Errorf("state = %s, want online after returning", updated.ConnectionState)
	}
}

func TestHeartbeatTimingMatchesTheSpecifiedPolicy(t *testing.T) {
	// spec.md section 7: about every five seconds, offline after about
	// fifteen.
	if workerapi.HeartbeatInterval != 5*time.Second {
		t.Errorf("HeartbeatInterval = %v", workerapi.HeartbeatInterval)
	}
	if workerapi.StaleAfter != 15*time.Second {
		t.Errorf("StaleAfter = %v", workerapi.StaleAfter)
	}
	if workerapi.StaleAfter <= workerapi.HeartbeatInterval {
		t.Error("a worker would be called offline before it could report")
	}
}

func TestListWorkersReportsOperationalState(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), WorkerVersion: "0.1.0", PendingUploads: 5}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	workers, err := env.repo.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 1 {
		t.Fatalf("workers = %d, want 1", len(workers))
	}
	if workers[0].ConnectionState != domain.WorkerOnline || workers[0].PendingUploads != 5 {
		t.Errorf("worker = %+v", workers[0])
	}
}

// spec.md section 22 counts worker state changes as auditable. A heartbeat
// every five seconds is not an event; a transition is.

func auditActions(t *testing.T, env *uploadEnv, entityID string) []string {
	t.Helper()
	records, err := env.repo.ListAuditRecords(context.Background(), store.AuditFilter{
		EntityType: "worker", EntityID: entityID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	actions := make([]string, 0, len(records))
	for _, record := range records {
		actions = append(actions, record.Action)
	}
	return actions
}

func TestWorkerStateChangesAreAudited(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	if _, err := env.client.Register(ctx, &workerv1.RegisterRequest{
		WorkerName: worker.Name, WorkerVersion: "0.1.0"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for range 3 {
		if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
			WorkerId: worker.ID.String(), WorkerVersion: "0.1.0"}); err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
	}

	actions := auditActions(t, env, worker.ID.String())
	online := 0
	registered := 0
	for _, action := range actions {
		switch action {
		case "worker.online":
			online++
		case "worker.registered":
			registered++
		}
	}
	if registered != 1 {
		t.Errorf("worker.registered recorded %d times, want 1: %v", registered, actions)
	}
	// Three heartbeats, one transition: the trail records the change, not the
	// traffic.
	if online != 1 {
		t.Errorf("worker.online recorded %d times, want 1: %v", online, actions)
	}
}

// The record carries no actor, because no person did it. That is only
// possible because the audit trail keeps its actor column free of a foreign
// key (migration 00006).
func TestASystemAuditRecordHasNoActor(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()
	worker := env.newWorker(t)

	if _, err := env.client.Heartbeat(ctx, &workerv1.HeartbeatRequest{
		WorkerId: worker.ID.String(), WorkerVersion: "0.1.0"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	records, err := env.repo.ListAuditRecords(ctx, store.AuditFilter{
		EntityType: "worker", EntityID: worker.ID.String(), Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no audit record was written")
	}
	if records[0].ActorUserID != nil {
		t.Errorf("actor = %v, want none", records[0].ActorUserID)
	}
}
