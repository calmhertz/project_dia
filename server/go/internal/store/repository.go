package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/domain"
)

var ErrNotFound = errors.New("not found")

var ErrStationOverlap = errors.New("station reservation overlaps an existing pass")

var ErrReferenced = errors.New("other records still reference this")

var ErrDuplicate = errors.New("duplicate record")

var ErrSerialization = errors.New("serialization conflict")

const (
	serializationFailure = "40001"
	deadlockDetected     = "40P01"
)

const createPassRetries = 5

const overlapConstraintName = "passes_no_station_overlap"

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Repository struct {
	db querier
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pool, pool: pool}
}

func (r *Repository) InTx(ctx context.Context, fn func(*Repository) error) error {
	if r.pool == nil {
		return errors.New("nested transactions are not supported")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(&Repository{db: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.ConstraintName == overlapConstraintName:
			return ErrStationOverlap
		case pgErr.Code == serializationFailure, pgErr.Code == deadlockDetected:
			return ErrSerialization
		case pgErr.Code == "23505":
			return fmt.Errorf("%w: %s", ErrDuplicate, pgErr.ConstraintName)
		case pgErr.Code == foreignKeyViolation:
			return fmt.Errorf("%w: %s", ErrReferenced, pgErr.ConstraintName)
		}
	}
	return err
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func toJSON(value map[string]any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(value)
}

func seconds(d time.Duration) float64 { return d.Seconds() }

func duration(secs float64) time.Duration {
	return time.Duration(secs * float64(time.Second))
}


const userColumns = `id, username, password_hash, role, must_change_password, created_at, updated_at`

func (r *Repository) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, must_change_password)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userColumns,
		user.Username, user.PasswordHash, user.Role, user.MustChangePassword)
	return scanUser(row)
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	row := r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	return scanUser(row)
}

func (r *Repository) GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row := r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func scanUser(row pgx.Row) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role,
		&user.MustChangePassword, &user.CreatedAt, &user.UpdatedAt)
	return user, translate(err)
}


const stationColumns = `id, name, latitude, longitude, altitude_m, timezone, active_rf_band, created_at, updated_at`

func (r *Repository) CreateStation(ctx context.Context, station domain.Station) (domain.Station, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO stations (name, latitude, longitude, altitude_m, timezone, active_rf_band)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+stationColumns,
		station.Name, station.Latitude, station.Longitude, station.AltitudeM,
		station.Timezone, station.ActiveRFBand)
	return scanStation(row)
}

func (r *Repository) GetStationByID(ctx context.Context, id uuid.UUID) (domain.Station, error) {
	row := r.db.QueryRow(ctx, `SELECT `+stationColumns+` FROM stations WHERE id = $1`, id)
	return scanStation(row)
}

func scanStation(row pgx.Row) (domain.Station, error) {
	var s domain.Station
	err := row.Scan(&s.ID, &s.Name, &s.Latitude, &s.Longitude, &s.AltitudeM,
		&s.Timezone, &s.ActiveRFBand, &s.CreatedAt, &s.UpdatedAt)
	return s, translate(err)
}


const schedulingConfigColumns = `
	id, station_id,
	EXTRACT(EPOCH FROM minimum_lead_time)::double precision,
	EXTRACT(EPOCH FROM pre_pass_buffer)::double precision,
	EXTRACT(EPOCH FROM post_pass_buffer)::double precision,
	EXTRACT(EPOCH FROM recording_pre_roll)::double precision,
	EXTRACT(EPOCH FROM recording_post_roll)::double precision,
	minimum_elevation_degrees, created_by, created_at, effective_from`

func (r *Repository) CreateSchedulingConfig(ctx context.Context, cfg domain.SchedulingConfig) (domain.SchedulingConfig, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO scheduling_configs (
			station_id, minimum_lead_time, pre_pass_buffer, post_pass_buffer,
			recording_pre_roll, recording_post_roll, minimum_elevation_degrees,
			created_by, effective_from)
		VALUES ($1,
			make_interval(secs => $2), make_interval(secs => $3), make_interval(secs => $4),
			make_interval(secs => $5), make_interval(secs => $6),
			$7, $8, $9)
		RETURNING `+schedulingConfigColumns,
		cfg.StationID, seconds(cfg.MinimumLeadTime), seconds(cfg.PrePassBuffer),
		seconds(cfg.PostPassBuffer), seconds(cfg.RecordingPreRoll), seconds(cfg.RecordingPostRoll),
		cfg.MinimumElevationDegrees, cfg.CreatedBy, cfg.EffectiveFrom)
	return scanSchedulingConfig(row)
}

func (r *Repository) LatestEffectiveSchedulingConfig(ctx context.Context, stationID uuid.UUID, at time.Time) (domain.SchedulingConfig, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+schedulingConfigColumns+`
		FROM scheduling_configs
		WHERE station_id = $1 AND effective_from <= $2
		ORDER BY created_at DESC
		LIMIT 1`, stationID, at)
	return scanSchedulingConfig(row)
}

func (r *Repository) PendingSchedulingConfig(ctx context.Context, stationID uuid.UUID,
	at time.Time) (domain.SchedulingConfig, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+schedulingConfigColumns+`
		FROM scheduling_configs
		WHERE station_id = $1
		  AND effective_from > $2
		  AND created_at > COALESCE((
			  SELECT max(created_at) FROM scheduling_configs
			  WHERE station_id = $1 AND effective_from <= $2
		  ), '-infinity'::timestamptz)
		ORDER BY created_at DESC
		LIMIT 1`, stationID, at)
	return scanSchedulingConfig(row)
}

func scanSchedulingConfig(row pgx.Row) (domain.SchedulingConfig, error) {
	var cfg domain.SchedulingConfig
	var lead, pre, post, preRoll, postRoll float64
	err := row.Scan(&cfg.ID, &cfg.StationID, &lead, &pre, &post, &preRoll, &postRoll,
		&cfg.MinimumElevationDegrees, &cfg.CreatedBy, &cfg.CreatedAt, &cfg.EffectiveFrom)
	if err != nil {
		return cfg, translate(err)
	}
	cfg.MinimumLeadTime = duration(lead)
	cfg.PrePassBuffer = duration(pre)
	cfg.PostPassBuffer = duration(post)
	cfg.RecordingPreRoll = duration(preRoll)
	cfg.RecordingPostRoll = duration(postRoll)
	return cfg, nil
}


const workerColumns = `id, station_id, name, worker_version, connection_state,
	last_seen_at, last_sync_at, desired_state_generation, pending_uploads,
	pending_reports, synced_generation, created_at, updated_at`

func (r *Repository) CreateWorker(ctx context.Context, worker domain.Worker) (domain.Worker, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO workers (station_id, name, worker_version, connection_state)
		VALUES ($1, $2, $3, $4)
		RETURNING `+workerColumns,
		worker.StationID, worker.Name, worker.WorkerVersion, worker.ConnectionState)
	return scanWorker(row)
}

func (r *Repository) GetWorkerByName(ctx context.Context, name string) (domain.Worker, error) {
	row := r.db.QueryRow(ctx, `SELECT `+workerColumns+` FROM workers WHERE name = $1`, name)
	return scanWorker(row)
}

func scanWorker(row interface{ Scan(...any) error }) (domain.Worker, error) {
	var w domain.Worker
	err := row.Scan(&w.ID, &w.StationID, &w.Name, &w.WorkerVersion, &w.ConnectionState,
		&w.LastSeenAt, &w.LastSyncAt, &w.DesiredStateGeneration, &w.PendingUploads,
		&w.PendingReports, &w.SyncedGeneration, &w.CreatedAt, &w.UpdatedAt)
	return w, translate(err)
}


const satelliteColumns = `id, norad_id, name, description, metadata, is_schedulable, created_at, updated_at`

func (r *Repository) CreateSatellite(ctx context.Context, satellite domain.Satellite) (domain.Satellite, error) {
	metadata, err := toJSON(satellite.Metadata)
	if err != nil {
		return domain.Satellite{}, fmt.Errorf("encode satellite metadata: %w", err)
	}
	row := r.db.QueryRow(ctx, `
		INSERT INTO satellites (norad_id, name, description, metadata, is_schedulable)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+satelliteColumns,
		satellite.NoradID, satellite.Name, satellite.Description, metadata, satellite.IsSchedulable)
	return scanSatellite(row)
}

func (r *Repository) GetSatelliteByNoradID(ctx context.Context, noradID int) (domain.Satellite, error) {
	row := r.db.QueryRow(ctx, `SELECT `+satelliteColumns+` FROM satellites WHERE norad_id = $1`, noradID)
	return scanSatellite(row)
}

func scanSatellite(row pgx.Row) (domain.Satellite, error) {
	var s domain.Satellite
	var metadata []byte
	err := row.Scan(&s.ID, &s.NoradID, &s.Name, &s.Description, &metadata,
		&s.IsSchedulable, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, translate(err)
	}
	if err := json.Unmarshal(metadata, &s.Metadata); err != nil {
		return s, fmt.Errorf("decode satellite metadata: %w", err)
	}
	return s, nil
}


const tleColumns = `id, satellite_id, line1, line2, epoch, source, fetched_at`

func (r *Repository) CreateTLERecord(ctx context.Context, record domain.TLERecord) (domain.TLERecord, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO tle_records (satellite_id, line1, line2, epoch, source)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+tleColumns,
		record.SatelliteID, record.Line1, record.Line2, record.Epoch, record.Source)
	return scanTLE(row)
}

func (r *Repository) LatestTLERecord(ctx context.Context, satelliteID uuid.UUID) (domain.TLERecord, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+tleColumns+` FROM tle_records
		WHERE satellite_id = $1
		ORDER BY epoch DESC, fetched_at DESC
		LIMIT 1`, satelliteID)
	return scanTLE(row)
}

func scanTLE(row pgx.Row) (domain.TLERecord, error) {
	var t domain.TLERecord
	err := row.Scan(&t.ID, &t.SatelliteID, &t.Line1, &t.Line2, &t.Epoch, &t.Source, &t.FetchedAt)
	return t, translate(err)
}


const passColumns = `id, station_id, satellite_id, requested_by, tle_record_id,
	scheduling_config_id, status, visibility, band, aos_at, los_at, tca_at,
	max_elevation_degrees, reserved_from, reserved_to, recording_mode,
	EXTRACT(EPOCH FROM recording_pre_roll)::double precision,
	EXTRACT(EPOCH FROM recording_post_roll)::double precision,
	pipeline_id, radio_settings, approved_by, approved_at,
	cancellation_reason, cancelled_by, cancelled_at, created_at, updated_at`

func (r *Repository) CreatePass(ctx context.Context, pass domain.Pass) (domain.Pass, error) {
	var lastErr error
	for attempt := 0; attempt < createPassRetries; attempt++ {
		created, err := r.createPassOnce(ctx, pass)
		if !errors.Is(err, ErrSerialization) {
			return created, err
		}
		lastErr = err
		if ctx.Err() != nil {
			return domain.Pass{}, ctx.Err()
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return domain.Pass{}, lastErr
}

func (r *Repository) createPassOnce(ctx context.Context, pass domain.Pass) (domain.Pass, error) {
	radio, err := toJSON(pass.RadioSettings)
	if err != nil {
		return domain.Pass{}, fmt.Errorf("encode radio settings: %w", err)
	}
	row := r.db.QueryRow(ctx, `
		INSERT INTO passes (
			station_id, satellite_id, requested_by, tle_record_id, scheduling_config_id,
			status, visibility, band, aos_at, los_at, tca_at, max_elevation_degrees,
			reserved_from, reserved_to, recording_mode,
			recording_pre_roll, recording_post_roll, pipeline_id, radio_settings)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			make_interval(secs => $16), make_interval(secs => $17), $18, $19)
		RETURNING `+passColumns,
		pass.StationID, pass.SatelliteID, pass.RequestedBy, pass.TLERecordID,
		pass.SchedulingConfigID, pass.Status, pass.Visibility, pass.Band,
		pass.AOSAt, pass.LOSAt, pass.TCAAt, pass.MaxElevationDegrees,
		pass.ReservedFrom, pass.ReservedTo, pass.RecordingMode,
		seconds(pass.RecordingPreRoll), seconds(pass.RecordingPostRoll),
		pass.PipelineID, radio)
	return scanPass(row)
}

func (r *Repository) GetPassByID(ctx context.Context, id uuid.UUID) (domain.Pass, error) {
	row := r.db.QueryRow(ctx, `SELECT `+passColumns+` FROM passes WHERE id = $1`, id)
	return scanPass(row)
}

func (r *Repository) SetPassStatus(ctx context.Context, id uuid.UUID, status domain.PassStatus) error {
	tag, err := r.db.Exec(ctx, `UPDATE passes SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPass(row pgx.Row) (domain.Pass, error) {
	var p domain.Pass
	var preRoll, postRoll float64
	var radio []byte
	err := row.Scan(&p.ID, &p.StationID, &p.SatelliteID, &p.RequestedBy, &p.TLERecordID,
		&p.SchedulingConfigID, &p.Status, &p.Visibility, &p.Band, &p.AOSAt, &p.LOSAt,
		&p.TCAAt, &p.MaxElevationDegrees, &p.ReservedFrom, &p.ReservedTo, &p.RecordingMode,
		&preRoll, &postRoll, &p.PipelineID, &radio, &p.ApprovedBy, &p.ApprovedAt,
		&p.CancellationReason, &p.CancelledBy, &p.CancelledAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, translate(err)
	}
	p.RecordingPreRoll = duration(preRoll)
	p.RecordingPostRoll = duration(postRoll)
	if err := json.Unmarshal(radio, &p.RadioSettings); err != nil {
		return p, fmt.Errorf("decode radio settings: %w", err)
	}
	return p, nil
}


func (r *Repository) AppendAudit(ctx context.Context, record domain.AuditRecord) (domain.AuditRecord, error) {
	previous, err := toJSON(record.PreviousState)
	if err != nil {
		return domain.AuditRecord{}, fmt.Errorf("encode previous state: %w", err)
	}
	next, err := toJSON(record.NewState)
	if err != nil {
		return domain.AuditRecord{}, fmt.Errorf("encode new state: %w", err)
	}
	row := r.db.QueryRow(ctx, `
		INSERT INTO audit_records (actor_user_id, action, entity_type, entity_id,
			previous_state, new_state, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		record.ActorUserID, record.Action, record.EntityType, record.EntityID,
		previous, next, record.Reason)
	if err := row.Scan(&record.ID, &record.CreatedAt); err != nil {
		return record, translate(err)
	}
	return record, nil
}

func (r *Repository) CountAuditRecords(ctx context.Context, entityType, entityID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM audit_records WHERE entity_type = $1 AND entity_id = $2`,
		entityType, entityID).Scan(&count)
	return count, translate(err)
}

type AuditFilter struct {
	Action string
	EntityType string
	EntityID   string
	Limit      int
}

func (r *Repository) ListAuditRecords(ctx context.Context, filter AuditFilter) ([]domain.AuditRecord, error) {
	query := `SELECT id, actor_user_id, action, entity_type, entity_id,
		previous_state, new_state, reason, created_at
		FROM audit_records WHERE true`
	args := []any{}

	if filter.Action != "" {
		args = append(args, filter.Action)
		query += ` AND action = $` + strconv.Itoa(len(args))
	}
	if filter.EntityType != "" {
		args = append(args, filter.EntityType)
		query += ` AND entity_type = $` + strconv.Itoa(len(args))
	}
	if filter.EntityID != "" {
		args = append(args, filter.EntityID)
		query += ` AND entity_id = $` + strconv.Itoa(len(args))
	}

	query += ` ORDER BY id DESC`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += ` LIMIT $` + strconv.Itoa(len(args))
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var records []domain.AuditRecord
	for rows.Next() {
		var record domain.AuditRecord
		var previous, next []byte
		if err := rows.Scan(&record.ID, &record.ActorUserID, &record.Action,
			&record.EntityType, &record.EntityID, &previous, &next,
			&record.Reason, &record.CreatedAt); err != nil {
			return nil, translate(err)
		}
		if len(previous) > 0 {
			_ = json.Unmarshal(previous, &record.PreviousState)
		}
		if len(next) > 0 {
			_ = json.Unmarshal(next, &record.NewState)
		}
		records = append(records, record)
	}
	return records, translate(rows.Err())
}
