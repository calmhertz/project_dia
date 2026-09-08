package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/domain"
)

// Recording is stored metadata for one captured file.
type Recording struct {
	RecordingID    string
	PassID         uuid.UUID
	WorkerID       *uuid.UUID
	StationID      uuid.UUID
	RelativePath   string
	StoredPath     string
	SizeBytes      int64
	ChecksumSHA256 string
	Status         string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	ReceivedAt     time.Time
}

const recordingColumns = `recording_id, pass_id, worker_id, station_id, relative_path,
	stored_path, size_bytes, checksum_sha256, status, started_at, finished_at, received_at`

// GetRecording returns one recording by its deterministic id.
func (r *Repository) GetRecording(ctx context.Context, recordingID string) (Recording, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+recordingColumns+` FROM recordings WHERE recording_id = $1`, recordingID)
	return scanRecording(row)
}

// InsertRecordingIfAbsent stores metadata unless the recording already exists.
//
// Reports whether this call created the row. A false return means the Server
// already held the recording, which is a successful upload, not an error
// (spec.md section 19.3).
func (r *Repository) InsertRecordingIfAbsent(ctx context.Context, recording Recording) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO recordings (recording_id, pass_id, worker_id, station_id,
			relative_path, stored_path, size_bytes, checksum_sha256, status,
			started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (recording_id) DO NOTHING`,
		recording.RecordingID, recording.PassID, recording.WorkerID, recording.StationID,
		recording.RelativePath, recording.StoredPath, recording.SizeBytes,
		recording.ChecksumSHA256, recording.Status, recording.StartedAt, recording.FinishedAt)
	if err != nil {
		return false, translate(err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListRecordingsForPass returns a pass's recordings.
func (r *Repository) ListRecordingsForPass(ctx context.Context, passID uuid.UUID) ([]Recording, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+recordingColumns+` FROM recordings WHERE pass_id = $1 ORDER BY relative_path`, passID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var recordings []Recording
	for rows.Next() {
		recording, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		recordings = append(recordings, recording)
	}
	return recordings, translate(rows.Err())
}

// CountRecordings reports how many recordings exist for a pass.
func (r *Repository) CountRecordings(ctx context.Context, passID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM recordings WHERE pass_id = $1`, passID).Scan(&count)
	return count, translate(err)
}

// SetPassOutcome records a Worker-reported execution result.
//
// Only a live pass moves: re-reporting an outcome for a pass that already
// reached a terminal state is ignored rather than rewriting history.
func (r *Repository) SetPassOutcome(ctx context.Context, passID uuid.UUID,
	status domain.PassStatus) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE passes SET status = $2
		WHERE id = $1 AND status IN ('approved', 'executing')`, passID, status)
	if err != nil {
		return false, translate(err)
	}
	return tag.RowsAffected() == 1, nil
}

func scanRecording(row interface{ Scan(...any) error }) (Recording, error) {
	var recording Recording
	err := row.Scan(&recording.RecordingID, &recording.PassID, &recording.WorkerID,
		&recording.StationID, &recording.RelativePath, &recording.StoredPath,
		&recording.SizeBytes, &recording.ChecksumSHA256, &recording.Status,
		&recording.StartedAt, &recording.FinishedAt, &recording.ReceivedAt)
	return recording, translate(err)
}

// RecordingWithPass is a recording alongside the pass it belongs to, which is
// what a recordings listing needs to be readable.
type RecordingWithPass struct {
	Recording
	SatelliteID uuid.UUID
	PassAOS     time.Time
	Visibility  domain.PassVisibility
	RequestedBy uuid.UUID
}

// ListRecordingsVisibleTo returns recordings the caller may see.
//
// Visibility is derived from the pass in SQL rather than filtered afterwards,
// so a private recording cannot leak through a listing (spec.md section 16).
// ownerID nil with includeAll means Admin or Root, who see everything.
func (r *Repository) ListRecordingsVisibleTo(ctx context.Context,
	ownerID *uuid.UUID, publicOnly bool, includeAll bool, limit int) ([]RecordingWithPass, error) {
	query := `
		SELECT ` + prefixed(recordingColumns, "r") + `,
			p.satellite_id, p.aos_at, p.visibility, p.requested_by
		FROM recordings r
		JOIN passes p ON p.id = r.pass_id
		WHERE `
	args := []any{}

	switch {
	case includeAll:
		query += `true`
	case publicOnly:
		query += `p.visibility = 'public'`
	case ownerID != nil:
		args = append(args, *ownerID)
		query += `p.requested_by = $1`
	default:
		return nil, nil
	}

	args = append(args, limit)
	query += ` ORDER BY r.received_at DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var out []RecordingWithPass
	for rows.Next() {
		var entry RecordingWithPass
		if err := rows.Scan(&entry.RecordingID, &entry.PassID, &entry.WorkerID,
			&entry.StationID, &entry.RelativePath, &entry.StoredPath,
			&entry.SizeBytes, &entry.ChecksumSHA256, &entry.Status,
			&entry.StartedAt, &entry.FinishedAt, &entry.ReceivedAt,
			&entry.SatelliteID, &entry.PassAOS, &entry.Visibility,
			&entry.RequestedBy); err != nil {
			return nil, translate(err)
		}
		out = append(out, entry)
	}
	return out, translate(rows.Err())
}

// prefixed qualifies a column list with a table alias.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for index, part := range parts {
		parts[index] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}
