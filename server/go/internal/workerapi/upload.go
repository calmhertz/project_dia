package workerapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/store"
)

// maxRecordingBytes bounds a single upload so a runaway Worker cannot fill the
// Server's disk in one call.
const maxRecordingBytes = 8 << 30 // 8 GiB

// UploadRecording receives one recording.
//
// Idempotency is the point of this method (spec.md section 19.3). The Worker
// computes a deterministic recording id, so a retry after a lost
// acknowledgement arrives with the same id and must not create a second
// logical recording. The content is written to a temporary file and moved into
// place only after the checksum matches, so a failed transfer never leaves a
// partial file where a good one should be.
func (s *Service) UploadRecording(stream workerv1.WorkerService_UploadRecordingServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "no upload metadata received")
	}
	metadata := first.GetMetadata()
	if metadata == nil {
		return status.Error(codes.InvalidArgument, "the first message must be metadata")
	}

	passID, stationID, err := s.validateUpload(metadata)
	if err != nil {
		return err
	}

	// A recording the Server already holds is an accepted retry. Answering
	// before reading the body saves transferring it again.
	if existing, err := s.repo.GetRecording(stream.Context(), metadata.GetRecordingId()); err == nil {
		s.logger.Info("recording already held; upload is a retry",
			slog.String("recording_id", existing.RecordingID))
		return stream.SendAndClose(&workerv1.UploadRecordingResponse{
			RecordingId: existing.RecordingID,
			Stored:      false,
			SizeBytes:   uint64(existing.SizeBytes),
		})
	} else if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error("look up recording", slog.String("error", err.Error()))
		return status.Error(codes.Internal, "could not check for an existing recording")
	}

	// Refuse before reading the body rather than filling the disk and
	// failing halfway. The Worker keeps its copy and retries, so a refusal
	// costs nothing but a delay (worker-spec section 15).
	if err := s.files.EnsureSpaceFor(int64(metadata.GetSizeBytes())); err != nil {
		s.logger.Warn("refusing upload; the recording disk is nearly full",
			slog.String("pass_id", passID.String()),
			slog.Uint64("size_bytes", metadata.GetSizeBytes()))
		return status.Error(codes.ResourceExhausted,
			"the server has no room for this recording")
	}

	destination, err := s.files.RecordingFilePath(
		stationID.String(), passID.String(), metadata.GetRelativePath())
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid recording path")
	}

	written, checksum, err := s.receiveContent(stream, destination)
	if err != nil {
		return err
	}

	if checksum != metadata.GetChecksumSha256() {
		// A corrupted transfer must not be stored; the Worker keeps its copy
		// and can retry.
		_ = os.Remove(destination)
		return status.Error(codes.DataLoss, "checksum mismatch; the transfer was corrupted")
	}
	if uint64(written) != metadata.GetSizeBytes() {
		_ = os.Remove(destination)
		return status.Errorf(codes.DataLoss,
			"size mismatch: received %d bytes, expected %d", written, metadata.GetSizeBytes())
	}

	recording := store.Recording{
		RecordingID:    metadata.GetRecordingId(),
		PassID:         passID,
		StationID:      stationID,
		RelativePath:   metadata.GetRelativePath(),
		StoredPath:     destination,
		SizeBytes:      written,
		ChecksumSHA256: checksum,
		Status:         "stored",
	}
	if workerID, err := uuid.Parse(metadata.GetWorkerId()); err == nil {
		recording.WorkerID = &workerID
	}
	if started := metadata.GetStartedAt(); started != nil {
		at := started.AsTime()
		recording.StartedAt = &at
	}
	if finished := metadata.GetFinishedAt(); finished != nil {
		at := finished.AsTime()
		recording.FinishedAt = &at
	}

	inserted, err := s.repo.InsertRecordingIfAbsent(stream.Context(), recording)
	if err != nil {
		s.logger.Error("store recording metadata", slog.String("error", err.Error()))
		return status.Error(codes.Internal, "could not store the recording")
	}

	s.logger.Info("recording stored",
		slog.String("recording_id", recording.RecordingID),
		slog.Int64("size_bytes", written),
		slog.Bool("inserted", inserted))

	return stream.SendAndClose(&workerv1.UploadRecordingResponse{
		RecordingId: recording.RecordingID,
		// A concurrent duplicate lost the insert race; still a success.
		Stored:    inserted,
		SizeBytes: uint64(written),
	})
}

// receiveContent streams the body to a temporary file, then moves it into
// place. Writing directly to the destination would leave a partial file behind
// if the connection dropped.
func (s *Service) receiveContent(stream workerv1.WorkerService_UploadRecordingServer,
	destination string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		s.logger.Error("create recording directory", slog.String("error", err.Error()))
		return 0, "", status.Error(codes.Internal, "could not prepare storage")
	}

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".upload-*")
	if err != nil {
		s.logger.Error("create temporary file", slog.String("error", err.Error()))
		return 0, "", status.Error(codes.Internal, "could not prepare storage")
	}
	temporaryPath := temporary.Name()
	// Remove the temporary file unless it was renamed into place.
	defer func() {
		temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return 0, "", status.Error(codes.Internal, "could not prepare storage")
	}

	hash := sha256.New()
	var written int64

	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, "", status.Error(codes.Aborted, "the upload was interrupted")
		}
		if message.GetMetadata() != nil {
			return 0, "", status.Error(codes.InvalidArgument, "metadata may only be sent once")
		}

		chunk := message.GetChunk()
		written += int64(len(chunk))
		if written > maxRecordingBytes {
			return 0, "", status.Error(codes.ResourceExhausted, "the recording is too large")
		}
		if _, err := temporary.Write(chunk); err != nil {
			s.logger.Error("write recording", slog.String("error", err.Error()))
			return 0, "", status.Error(codes.Internal, "could not write the recording")
		}
		hash.Write(chunk)
	}

	// Flush to disk before the rename, so a crash cannot leave a named file
	// with unwritten content.
	if err := temporary.Sync(); err != nil {
		return 0, "", status.Error(codes.Internal, "could not flush the recording")
	}
	if err := temporary.Close(); err != nil {
		return 0, "", status.Error(codes.Internal, "could not close the recording")
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		s.logger.Error("move recording into place", slog.String("error", err.Error()))
		return 0, "", status.Error(codes.Internal, "could not store the recording")
	}

	return written, hex.EncodeToString(hash.Sum(nil)), nil
}

// validateUpload checks the metadata before any content is accepted.
func (s *Service) validateUpload(metadata *workerv1.RecordingMetadata) (uuid.UUID, uuid.UUID, error) {
	if !isHex64(metadata.GetRecordingId()) {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument,
			"recording_id must be a sha-256 hex digest")
	}
	if !isHex64(metadata.GetChecksumSha256()) {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument,
			"checksum_sha256 must be a sha-256 hex digest")
	}
	passID, err := uuid.Parse(metadata.GetPassId())
	if err != nil {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "invalid pass id")
	}
	stationID, err := uuid.Parse(metadata.GetStationId())
	if err != nil {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "invalid station id")
	}
	if metadata.GetRelativePath() == "" {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "relative_path is required")
	}
	return passID, stationID, nil
}

func isHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ReportExecutions records pass outcomes the Worker gathered, including while
// the Server was unreachable.
//
// Re-reporting is safe: a pass that already reached a terminal state is left
// alone rather than rewritten (spec.md section 14).
func (s *Service) ReportExecutions(ctx context.Context,
	request *workerv1.ReportExecutionsRequest) (*workerv1.ReportExecutionsResponse, error) {
	accepted := 0

	for _, record := range request.GetRecords() {
		passID, err := uuid.Parse(record.GetPassId())
		if err != nil {
			s.logger.Warn("skipping execution record with an invalid pass id")
			continue
		}
		passStatus, ok := passStatusFor(record.GetState())
		if !ok {
			s.logger.Warn("skipping execution record with an unknown state",
				slog.String("state", record.GetState()))
			continue
		}

		changed, err := s.repo.SetPassOutcome(ctx, passID, passStatus)
		if err != nil {
			s.logger.Error("record pass outcome",
				slog.String("pass_id", passID.String()), slog.String("error", err.Error()))
			return nil, status.Error(codes.Internal, "could not record the outcome")
		}
		// Counted either way: an already-recorded outcome is accepted, so the
		// Worker stops resending it.
		accepted++
		if changed {
			s.logger.Info("pass outcome recorded",
				slog.String("pass_id", passID.String()),
				slog.String("status", string(passStatus)))
			if _, err := s.repo.AppendAudit(ctx, domain.AuditRecord{
				Action: "pass." + string(passStatus), EntityType: "pass",
				EntityID: passID.String(),
				NewState: map[string]any{
					"reported_by_worker": request.GetWorkerId(),
					"detail":             record.GetDetail(),
					"recorded_at":        recordedAt(record),
				},
			}); err != nil {
				s.logger.Error("audit pass outcome", slog.String("error", err.Error()))
			}
		}
	}

	return &workerv1.ReportExecutionsResponse{Accepted: uint32(accepted)}, nil
}

// passStatusFor maps a Worker execution state onto the Server's pass
// lifecycle. Worker-only states have no Server equivalent and are ignored.
func passStatusFor(workerState string) (domain.PassStatus, bool) {
	switch workerState {
	case "completed":
		return domain.PassCompleted, true
	case "failed":
		return domain.PassFailed, true
	case "missed":
		return domain.PassMissed, true
	case "cancelled":
		// The Server already knows: cancellation originates there.
		return "", false
	default:
		return "", false
	}
}

func recordedAt(record *workerv1.ExecutionRecord) string {
	if record.GetRecordedAt() == nil {
		return ""
	}
	return record.GetRecordedAt().AsTime().Format(time.RFC3339)
}
