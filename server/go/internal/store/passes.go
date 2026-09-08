package store

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/domain"
)

// PassFilter narrows a pass listing.
type PassFilter struct {
	// RequestedBy limits to one owner. Nil means no owner filter.
	RequestedBy *uuid.UUID
	// Statuses limits to these states. Empty means any state.
	Statuses []domain.PassStatus
	// From and To bound the pass by AOS. Zero means unbounded.
	From time.Time
	To   time.Time
	// OnlyPublic limits to publicly visible passes.
	OnlyPublic bool
	Limit      int
}

// ListPasses returns passes matching the filter, soonest first.
func (r *Repository) ListPasses(ctx context.Context, filter PassFilter) ([]domain.Pass, error) {
	query := `SELECT ` + passColumns + ` FROM passes WHERE true`
	args := []any{}

	if filter.RequestedBy != nil {
		args = append(args, *filter.RequestedBy)
		query += ` AND requested_by = $` + itoa(len(args))
	}
	if len(filter.Statuses) > 0 {
		args = append(args, statusStrings(filter.Statuses))
		query += ` AND status = ANY($` + itoa(len(args)) + `::pass_status[])`
	}
	if !filter.From.IsZero() {
		args = append(args, filter.From)
		query += ` AND aos_at >= $` + itoa(len(args))
	}
	if !filter.To.IsZero() {
		args = append(args, filter.To)
		query += ` AND aos_at <= $` + itoa(len(args))
	}
	if filter.OnlyPublic {
		query += ` AND visibility = 'public'`
	}

	query += ` ORDER BY aos_at`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += ` LIMIT $` + itoa(len(args))
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var passes []domain.Pass
	for rows.Next() {
		pass, err := scanPass(rows)
		if err != nil {
			return nil, err
		}
		passes = append(passes, pass)
	}
	return passes, translate(rows.Err())
}

// FindConflictingPasses returns live passes whose reservation overlaps the
// given window on the same station.
//
// The database constraint is what actually prevents a conflict; this exists to
// report which passes are in the way, and for Root override to act on them.
func (r *Repository) FindConflictingPasses(ctx context.Context, stationID uuid.UUID,
	from, to time.Time, liveStatuses []domain.PassStatus) ([]domain.Pass, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+passColumns+` FROM passes
		WHERE station_id = $1
		  AND status = ANY($2::pass_status[])
		  AND reservation && tstzrange($3, $4, '[)')
		ORDER BY reserved_from`,
		stationID, statusStrings(liveStatuses), from, to)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var passes []domain.Pass
	for rows.Next() {
		pass, err := scanPass(rows)
		if err != nil {
			return nil, err
		}
		passes = append(passes, pass)
	}
	return passes, translate(rows.Err())
}

// ApprovePass records an approval decision.
func (r *Repository) ApprovePass(ctx context.Context, id, approvedBy uuid.UUID, at time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE passes SET status = 'approved', approved_by = $2, approved_at = $3
		WHERE id = $1 AND status = 'pending_approval'`, id, approvedBy, at)
	if err != nil {
		return translate(err)
	}
	// Zero rows means the pass moved on between read and write; the caller
	// re-reads rather than assuming success.
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RejectPass records a rejection.
func (r *Repository) RejectPass(ctx context.Context, id, decidedBy uuid.UUID, at time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE passes SET status = 'rejected', approved_by = $2, approved_at = $3
		WHERE id = $1 AND status = 'pending_approval'`, id, decidedBy, at)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CancelPass marks a pass cancelled with a reason.
//
// The row is never deleted: an overridden or cancelled pass stays auditable
// (spec.md section 13.7).
func (r *Repository) CancelPass(ctx context.Context, id, cancelledBy uuid.UUID,
	reason domain.CancellationReason, at time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE passes
		SET status = 'cancelled', cancellation_reason = $2, cancelled_by = $3, cancelled_at = $4
		WHERE id = $1 AND status IN ('pending_approval', 'approved')`,
		id, reason, cancelledBy, at)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Workers -----------------------------------------------------------------

// GetStationWorker returns the worker bound to a station.
func (r *Repository) GetStationWorker(ctx context.Context, stationID uuid.UUID) (domain.Worker, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+workerColumns+` FROM workers WHERE station_id = $1 ORDER BY created_at LIMIT 1`,
		stationID)
	return scanWorker(row)
}

// SetWorkerConnectionState records reachability. V12 drives this from the
// heartbeat; until then an operator or a test sets it.
func (r *Repository) SetWorkerConnectionState(ctx context.Context, workerID uuid.UUID,
	state domain.WorkerConnectionState, seenAt *time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE workers SET connection_state = $2, last_seen_at = COALESCE($3, last_seen_at)
		WHERE id = $1`, workerID, state, seenAt)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func statusStrings(statuses []domain.PassStatus) []string {
	out := make([]string, len(statuses))
	for index, status := range statuses {
		out[index] = string(status)
	}
	return out
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

// GetSchedulingConfigByID returns one specific configuration version, so a
// pass keeps the terms it was accepted under (spec.md section 13.8).
func (r *Repository) GetSchedulingConfigByID(ctx context.Context, id uuid.UUID) (domain.SchedulingConfig, error) {
	row := r.db.QueryRow(ctx, `SELECT `+schedulingConfigColumns+` FROM scheduling_configs WHERE id = $1`, id)
	return scanSchedulingConfig(row)
}

// Pipeline is a SatDump pipeline record.
type Pipeline struct {
	ID             uuid.UUID
	Name           string
	IsCustom       bool
	DefinitionPath string
	ChecksumSHA256 string
	// Definition is the pipeline JSON, loaded from disk when required.
	Definition string
}

// GetPipelineByID returns a pipeline record without its file contents.
func (r *Repository) GetPipelineByID(ctx context.Context, id uuid.UUID) (Pipeline, error) {
	var pipeline Pipeline
	var path, checksum *string
	err := r.db.QueryRow(ctx, `
		SELECT id, name, is_custom, definition_path, checksum_sha256
		FROM satdump_pipelines WHERE id = $1`, id).Scan(
		&pipeline.ID, &pipeline.Name, &pipeline.IsCustom, &path, &checksum)
	if err != nil {
		return Pipeline{}, translate(err)
	}
	if path != nil {
		pipeline.DefinitionPath = *path
	}
	if checksum != nil {
		pipeline.ChecksumSHA256 = *checksum
	}
	return pipeline, nil
}

// CreatePipeline registers a SatDump pipeline.
func (r *Repository) CreatePipeline(ctx context.Context, pipeline Pipeline, uploadedBy *uuid.UUID) (Pipeline, error) {
	var path, checksum *string
	if pipeline.DefinitionPath != "" {
		path = &pipeline.DefinitionPath
	}
	if pipeline.ChecksumSHA256 != "" {
		checksum = &pipeline.ChecksumSHA256
	}
	err := r.db.QueryRow(ctx, `
		INSERT INTO satdump_pipelines (name, is_custom, definition_path, checksum_sha256, uploaded_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		pipeline.Name, pipeline.IsCustom, path, checksum, uploadedBy).Scan(&pipeline.ID)
	return pipeline, translate(err)
}

// HeartbeatUpdate is what a Worker reports when it checks in.
type HeartbeatUpdate struct {
	WorkerID       uuid.UUID
	SeenAt         time.Time
	WorkerVersion  string
	PendingUploads int
	PendingReports int
}

// GetWorkerByID returns one worker.
func (r *Repository) GetWorkerByID(ctx context.Context, id uuid.UUID) (domain.Worker, error) {
	row := r.db.QueryRow(ctx, `SELECT `+workerColumns+` FROM workers WHERE id = $1`, id)
	return scanWorker(row)
}

// RecordHeartbeat marks a worker online and notes what it is carrying.
func (r *Repository) RecordHeartbeat(ctx context.Context, update HeartbeatUpdate) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE workers
		SET connection_state = 'online',
		    last_seen_at = $2,
		    worker_version = COALESCE(NULLIF($3, ''), worker_version),
		    pending_uploads = $4,
		    pending_reports = $5
		WHERE id = $1`,
		update.WorkerID, update.SeenAt, update.WorkerVersion,
		update.PendingUploads, update.PendingReports)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordWorkerSync notes that a worker has reached the current desired state.
func (r *Repository) RecordWorkerSync(ctx context.Context, workerID uuid.UUID,
	at time.Time, generation string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE workers SET last_sync_at = $2, synced_generation = $3 WHERE id = $1`,
		workerID, at, generation)
	return translate(err)
}

// MarkStaleWorkersOffline flips workers that have stopped reporting, returning
// the names of those that changed so the transition can be logged once.
func (r *Repository) MarkStaleWorkersOffline(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE workers SET connection_state = 'offline'
		WHERE connection_state = 'online'
		  AND (last_seen_at IS NULL OR last_seen_at < $1)
		RETURNING name`, cutoff)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, translate(err)
		}
		names = append(names, name)
	}
	return names, translate(rows.Err())
}

// ListWorkers returns every worker, for operational views.
func (r *Repository) ListWorkers(ctx context.Context) ([]domain.Worker, error) {
	rows, err := r.db.Query(ctx, `SELECT `+workerColumns+` FROM workers ORDER BY name`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var workers []domain.Worker
	for rows.Next() {
		worker, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	return workers, translate(rows.Err())
}

// ListPipelines returns the known pipelines, standard ones first.
func (r *Repository) ListPipelines(ctx context.Context) ([]Pipeline, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, is_custom, definition_path, checksum_sha256
		FROM satdump_pipelines ORDER BY is_custom, name`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var pipelines []Pipeline
	for rows.Next() {
		var pipeline Pipeline
		var path, checksum *string
		if err := rows.Scan(&pipeline.ID, &pipeline.Name, &pipeline.IsCustom,
			&path, &checksum); err != nil {
			return nil, translate(err)
		}
		if path != nil {
			pipeline.DefinitionPath = *path
		}
		if checksum != nil {
			pipeline.ChecksumSHA256 = *checksum
		}
		pipelines = append(pipelines, pipeline)
	}
	return pipelines, translate(rows.Err())
}

// ReplaceStandardPipelines records what a Worker's SatDump provides.
//
// Standard pipelines are a reflection of an installation, not a catalogue the
// Server owns, so the set is replaced wholesale. Custom uploads are never
// touched, and a standard pipeline a pass already references is kept so the
// pass stays readable.
func (r *Repository) ReplaceStandardPipelines(ctx context.Context, names []string) (int, error) {
	if len(names) == 0 {
		// An empty report means SatDump could not be read, not that the
		// station has no pipelines. Leave what is known in place.
		return 0, nil
	}

	tag, err := r.db.Exec(ctx, `
		DELETE FROM satdump_pipelines
		WHERE is_custom = false
		  AND name <> ALL($1)
		  AND id NOT IN (SELECT pipeline_id FROM passes WHERE pipeline_id IS NOT NULL)`,
		names)
	if err != nil {
		return 0, translate(err)
	}
	removed := tag.RowsAffected()

	for _, name := range names {
		if _, err := r.db.Exec(ctx, `
			INSERT INTO satdump_pipelines (name, is_custom)
			VALUES ($1, false)
			ON CONFLICT (name) DO NOTHING`, name); err != nil {
			return 0, translate(err)
		}
	}
	return int(removed), nil
}

// SetPipelineDefinitionPath records where a custom pipeline's JSON was
// written, once the file exists.
func (r *Repository) SetPipelineDefinitionPath(ctx context.Context, id uuid.UUID, path string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE satdump_pipelines SET definition_path = $2 WHERE id = $1`, id, path)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
