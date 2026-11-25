package workerapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"aagasa/internal/domain"
	"aagasa/internal/filestore"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
	"aagasa/internal/workerapi"
)

const argon2idHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHR2YWx1ZQ$aGFzaHZhbHVlaGFzaHZhbHVlaGFzaHZhbA"

const workerSecret = "fedcba9876543210fedcba9876543210"

type uploadEnv struct {
	client    workerv1.WorkerServiceClient
	dial      func(t *testing.T, options ...grpc.DialOption) workerv1.WorkerServiceClient
	repo      *store.Repository
	recordDir string

	passID      uuid.UUID
	stationID   uuid.UUID
	satelliteID uuid.UUID
	requesterID uuid.UUID
	tleID       uuid.UUID
	configID    uuid.UUID
	worker      *domain.Worker
}

func withSecret(secret string) grpc.DialOption {
	return grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, request, reply any,
		connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret)
		return invoker(ctx, method, request, reply, connection, options...)
	})
}

func withStreamSecret(secret string) grpc.DialOption {
	return grpc.WithChainStreamInterceptor(func(ctx context.Context, description *grpc.StreamDesc,
		connection *grpc.ClientConn, method string, streamer grpc.Streamer,
		options ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret)
		return streamer(ctx, description, connection, method, options...)
	})
}

func newUploadEnv(t *testing.T, configure ...func(*workerapi.Service)) *uploadEnv {
	t.Helper()
	ctx := context.Background()

	url := testsupport.NewDatabase(t)
	if err := store.MigrateUp(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	repo := store.NewRepository(pool)

	root := t.TempDir()
	files, err := filestore.New(filepath.Join(root, "recordings"), filepath.Join(root, "pipelines"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}

	env := &uploadEnv{repo: repo, recordDir: files.RecordingsRoot()}
	env.seed(t, ctx, repo)

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	service := workerapi.NewService(repo, files, logger, "test")
	for _, apply := range configure {
		apply(service)
	}

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(
		grpc.UnaryInterceptor(workerapi.SharedSecretInterceptor(workerSecret)),
		grpc.StreamInterceptor(workerapi.SharedSecretStreamInterceptor(workerSecret)))
	workerv1.RegisterWorkerServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	env.dial = func(t *testing.T, options ...grpc.DialOption) workerv1.WorkerServiceClient {
		t.Helper()
		options = append([]grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}, options...)
		connection, err := grpc.NewClient("passthrough:///bufnet", options...)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		return workerv1.NewWorkerServiceClient(connection)
	}
	env.client = env.dial(t, withSecret(workerSecret), withStreamSecret(workerSecret))
	return env
}

func (e *uploadEnv) seed(t *testing.T, ctx context.Context, repo *store.Repository) {
	t.Helper()
	user, err := repo.CreateUser(ctx, domain.User{
		Username: "operator", PasswordHash: argon2idHash, Role: domain.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	station, err := repo.CreateStation(ctx, domain.Station{
		Name: "SJCIT", Latitude: 13.4, Longitude: 77.7, AltitudeM: 915,
		Timezone: "UTC", ActiveRFBand: domain.BandVHF})
	if err != nil {
		t.Fatalf("create station: %v", err)
	}
	e.stationID = station.ID
	e.requesterID = user.ID

	config, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: station.ID, MinimumLeadTime: time.Minute,
		MinimumElevationDegrees: 10, EffectiveFrom: time.Now().UTC().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("scheduling config: %v", err)
	}
	e.configID = config.ID
	satellite, err := repo.CreateSatellite(ctx, domain.Satellite{
		NoradID: 25544, Name: "ISS", IsSchedulable: true})
	if err != nil {
		t.Fatalf("create satellite: %v", err)
	}
	e.satelliteID = satellite.ID
	tle, err := repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: satellite.ID,
		Line1:       "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990",
		Line2:       "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298",
		Epoch:       time.Now().UTC(), Source: domain.SourceCelestrak})
	if err != nil {
		t.Fatalf("create tle: %v", err)
	}
	e.tleID = tle.ID

	aos := time.Now().UTC().Add(2 * time.Hour)
	pass, err := repo.CreatePass(ctx, domain.Pass{
		StationID: station.ID, SatelliteID: satellite.ID, RequestedBy: user.ID,
		TLERecordID: tle.ID, SchedulingConfigID: config.ID,
		Status: domain.PassApproved, Visibility: domain.VisibilityPrivate,
		Band: domain.BandVHF, AOSAt: aos, LOSAt: aos.Add(10 * time.Minute),
		MaxElevationDegrees: 31,
		ReservedFrom:        aos.Add(-time.Minute), ReservedTo: aos.Add(11 * time.Minute),
		RecordingMode: domain.RecordRaw})
	if err != nil {
		t.Fatalf("create pass: %v", err)
	}
	e.passID = pass.ID
}

func recordingIDFor(passID, relativePath string) string {
	digest := sha256.New()
	digest.Write([]byte(passID))
	digest.Write([]byte{0})
	digest.Write([]byte(relativePath))
	return hex.EncodeToString(digest.Sum(nil))
}

func (e *uploadEnv) upload(t *testing.T, relativePath string, content []byte,
	mutate func(*workerv1.RecordingMetadata)) (*workerv1.UploadRecordingResponse, error) {
	t.Helper()
	return e.uploadWith(t, e.client, relativePath, content, mutate)
}

func (e *uploadEnv) uploadWith(t *testing.T, client workerv1.WorkerServiceClient,
	relativePath string, content []byte,
	mutate func(*workerv1.RecordingMetadata)) (*workerv1.UploadRecordingResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	worker := e.newWorker(t)
	stream, err := client.UploadRecording(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	checksum := sha256.Sum256(content)
	metadata := &workerv1.RecordingMetadata{
		RecordingId:    recordingIDFor(e.passID.String(), relativePath),
		PassId:         e.passID.String(),
		WorkerId:       worker.ID.String(),
		StationId:      e.stationID.String(),
		RelativePath:   relativePath,
		SizeBytes:      uint64(len(content)),
		ChecksumSha256: hex.EncodeToString(checksum[:]),
	}
	if mutate != nil {
		mutate(metadata)
	}

	if err := stream.Send(&workerv1.UploadRecordingRequest{
		Payload: &workerv1.UploadRecordingRequest_Metadata{Metadata: metadata},
	}); err != nil {
		return nil, sendError(stream, err)
	}
	for offset := 0; offset < len(content); offset += 4096 {
		end := offset + 4096
		if end > len(content) {
			end = len(content)
		}
		if err := stream.Send(&workerv1.UploadRecordingRequest{
			Payload: &workerv1.UploadRecordingRequest_Chunk{Chunk: content[offset:end]},
		}); err != nil {
			return nil, sendError(stream, err)
		}
	}
	return stream.CloseAndRecv()
}

func TestUploadStoresTheRecording(t *testing.T) {
	env := newUploadEnv(t)
	content := []byte("baseband content")

	response, err := env.upload(t, "baseband.ziq", content, nil)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !response.GetStored() {
		t.Error("Stored = false on a first upload")
	}
	if response.GetSizeBytes() != uint64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", response.GetSizeBytes(), len(content))
	}

	recording, err := env.repo.GetRecording(context.Background(), response.GetRecordingId())
	if err != nil {
		t.Fatalf("GetRecording: %v", err)
	}
	stored, err := os.ReadFile(recording.StoredPath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(stored) != string(content) {
		t.Error("stored content does not match what was sent")
	}
}

func TestRepeatedUploadCreatesNoDuplicate(t *testing.T) {
	env := newUploadEnv(t)
	content := []byte("the same recording, sent three times")

	first, err := env.upload(t, "baseband.ziq", content, nil)
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if !first.GetStored() {
		t.Error("the first upload did not store")
	}

	for attempt := 0; attempt < 2; attempt++ {
		retry, err := env.upload(t, "baseband.ziq", content, nil)
		if err != nil {
			t.Fatalf("retry %d: %v", attempt, err)
		}
		if retry.GetStored() {
			t.Errorf("retry %d stored a second copy", attempt)
		}
		if retry.GetRecordingId() != first.GetRecordingId() {
			t.Error("a retry produced a different recording id")
		}
	}

	count, err := env.repo.CountRecordings(context.Background(), env.passID)
	if err != nil {
		t.Fatalf("CountRecordings: %v", err)
	}
	if count != 1 {
		t.Errorf("recordings = %d, want exactly 1", count)
	}
}

func TestSeveralOutputsFromOnePassAreDistinct(t *testing.T) {
	env := newUploadEnv(t)

	if _, err := env.upload(t, "baseband.ziq", []byte("raw"), nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := env.upload(t, "images/channel1.png", []byte("decoded"), nil); err != nil {
		t.Fatalf("second: %v", err)
	}

	count, err := env.repo.CountRecordings(context.Background(), env.passID)
	if err != nil {
		t.Fatalf("CountRecordings: %v", err)
	}
	if count != 2 {
		t.Errorf("recordings = %d, want 2", count)
	}
}

func TestChecksumMismatchIsRejected(t *testing.T) {
	env := newUploadEnv(t)

	_, err := env.upload(t, "baseband.ziq", []byte("actual content"), func(m *workerv1.RecordingMetadata) {
		m.ChecksumSha256 = hex.EncodeToString(make([]byte, 32))
	})
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("error = %v, want DataLoss", err)
	}

	count, err := env.repo.CountRecordings(context.Background(), env.passID)
	if err != nil {
		t.Fatalf("CountRecordings: %v", err)
	}
	if count != 0 {
		t.Errorf("a corrupted upload was stored: %d recordings", count)
	}
}

func TestSizeMismatchIsRejected(t *testing.T) {
	env := newUploadEnv(t)

	_, err := env.upload(t, "baseband.ziq", []byte("content"), func(m *workerv1.RecordingMetadata) {
		m.SizeBytes = 999999
	})
	if status.Code(err) != codes.DataLoss {
		t.Errorf("error = %v, want DataLoss", err)
	}
}

func TestARejectedUploadLeavesNoFileBehind(t *testing.T) {
	env := newUploadEnv(t)

	_, _ = env.upload(t, "baseband.ziq", []byte("content"), func(m *workerv1.RecordingMetadata) {
		m.ChecksumSha256 = hex.EncodeToString(make([]byte, 32))
	})

	var found []string
	_ = filepath.Walk(env.recordDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("files left behind after a rejected upload: %v", found)
	}
}

func TestMalformedMetadataIsRejected(t *testing.T) {
	env := newUploadEnv(t)

	cases := map[string]func(*workerv1.RecordingMetadata){
		"bad recording id": func(m *workerv1.RecordingMetadata) { m.RecordingId = "not-a-digest" },
		"bad checksum":     func(m *workerv1.RecordingMetadata) { m.ChecksumSha256 = "short" },
		"bad pass id":      func(m *workerv1.RecordingMetadata) { m.PassId = "not-a-uuid" },
		"bad station id":   func(m *workerv1.RecordingMetadata) { m.StationId = "nope" },
		"empty path":       func(m *workerv1.RecordingMetadata) { m.RelativePath = "" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := env.upload(t, "baseband.ziq", []byte("content"), mutate)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("error = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	env := newUploadEnv(t)

	for _, hostile := range []string{"../escape.bin", "a/../../escape.bin", "/etc/passwd"} {
		_, err := env.upload(t, hostile, []byte("content"), nil)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("path %q: error = %v, want InvalidArgument", hostile, err)
		}
	}
}

func TestReportExecutionsRecordsTheOutcome(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()

	response, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
		WorkerId: env.newWorker(t).ID.String(),
		Records: []*workerv1.ExecutionRecord{
			{PassId: env.passID.String(), State: "completed", Detail: "decoded 42 frames"},
		},
	})
	if err != nil {
		t.Fatalf("ReportExecutions: %v", err)
	}
	if response.GetAccepted() != 1 {
		t.Errorf("Accepted = %d, want 1", response.GetAccepted())
	}

	pass, err := env.repo.GetPassByID(ctx, env.passID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	if pass.Status != domain.PassCompleted {
		t.Errorf("status = %s, want completed", pass.Status)
	}
}

func TestReportingTheSameOutcomeTwiceIsSafe(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()

	record := &workerv1.ExecutionRecord{PassId: env.passID.String(), State: "completed"}
	for attempt := 0; attempt < 3; attempt++ {
		response, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
			WorkerId: env.newWorker(t).ID.String(),
			Records:  []*workerv1.ExecutionRecord{record}})
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if response.GetAccepted() != 1 {
			t.Errorf("attempt %d accepted = %d", attempt, response.GetAccepted())
		}
	}

	pass, err := env.repo.GetPassByID(ctx, env.passID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	if pass.Status != domain.PassCompleted {
		t.Errorf("status = %s, want completed", pass.Status)
	}
}

func TestATerminalPassIsNotRewritten(t *testing.T) {
	env := newUploadEnv(t)
	ctx := context.Background()

	if _, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
		WorkerId: env.newWorker(t).ID.String(),
		Records: []*workerv1.ExecutionRecord{
			{PassId: env.passID.String(), State: "completed"}}}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if _, err := env.client.ReportExecutions(ctx, &workerv1.ReportExecutionsRequest{
		WorkerId: env.newWorker(t).ID.String(),
		Records: []*workerv1.ExecutionRecord{
			{PassId: env.passID.String(), State: "failed"}}}); err != nil {
		t.Fatalf("second report: %v", err)
	}

	pass, err := env.repo.GetPassByID(ctx, env.passID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	if pass.Status != domain.PassCompleted {
		t.Errorf("status = %s; a terminal pass was rewritten", pass.Status)
	}
}

func TestUnknownOutcomeStatesAreIgnored(t *testing.T) {
	env := newUploadEnv(t)

	response, err := env.client.ReportExecutions(context.Background(),
		&workerv1.ReportExecutionsRequest{
			WorkerId: env.newWorker(t).ID.String(),
			Records: []*workerv1.ExecutionRecord{
				{PassId: env.passID.String(), State: "nonsense"},
				{PassId: "not-a-uuid", State: "completed"},
			}})
	if err != nil {
		t.Fatalf("ReportExecutions: %v", err)
	}
	if response.GetAccepted() != 0 {
		t.Errorf("Accepted = %d, want 0", response.GetAccepted())
	}
}

// sendError resolves io.EOF from Send, which only means the server already
// ended the stream, into the status the server actually returned.
func sendError(stream workerv1.WorkerService_UploadRecordingClient, err error) error {
	if !errors.Is(err, io.EOF) {
		return err
	}
	_, err = stream.CloseAndRecv()
	return err
}
