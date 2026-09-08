package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"aagasa/internal/domain"
)

// ListSatellites returns the catalogue ordered by catalog number.
func (r *Repository) ListSatellites(ctx context.Context) ([]domain.Satellite, error) {
	rows, err := r.db.Query(ctx, `SELECT `+satelliteColumns+` FROM satellites ORDER BY norad_id`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var satellites []domain.Satellite
	for rows.Next() {
		var s domain.Satellite
		var metadata []byte
		if err := rows.Scan(&s.ID, &s.NoradID, &s.Name, &s.Description, &metadata,
			&s.IsSchedulable, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		if err := json.Unmarshal(metadata, &s.Metadata); err != nil {
			return nil, err
		}
		satellites = append(satellites, s)
	}
	return satellites, translate(rows.Err())
}

// GetSatelliteByID returns one satellite.
func (r *Repository) GetSatelliteByID(ctx context.Context, id uuid.UUID) (domain.Satellite, error) {
	row := r.db.QueryRow(ctx, `SELECT `+satelliteColumns+` FROM satellites WHERE id = $1`, id)
	return scanSatellite(row)
}

// UpdateSatellite changes the operator-editable fields.
func (r *Repository) UpdateSatellite(ctx context.Context, id uuid.UUID, name, description string, schedulable bool) (domain.Satellite, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE satellites SET name = $2, description = $3, is_schedulable = $4
		WHERE id = $1
		RETURNING `+satelliteColumns, id, name, description, schedulable)
	return scanSatellite(row)
}

// SetSatelliteMetadata replaces the enrichment metadata.
func (r *Repository) SetSatelliteMetadata(ctx context.Context, id uuid.UUID, metadata map[string]any) error {
	encoded, err := toJSON(metadata)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `UPDATE satellites SET metadata = $2 WHERE id = $1`, id, encoded)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSatellite removes a satellite. The database refuses while any pass
// references it, preserving history (spec.md section 21).
func (r *Repository) DeleteSatellite(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM satellites WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTLERecords returns a satellite's orbital data versions, newest first.
func (r *Repository) ListTLERecords(ctx context.Context, satelliteID uuid.UUID, limit int) ([]domain.TLERecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+tleColumns+` FROM tle_records
		WHERE satellite_id = $1 ORDER BY epoch DESC, fetched_at DESC LIMIT $2`,
		satelliteID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var records []domain.TLERecord
	for rows.Next() {
		var t domain.TLERecord
		if err := rows.Scan(&t.ID, &t.SatelliteID, &t.Line1, &t.Line2, &t.Epoch,
			&t.Source, &t.FetchedAt); err != nil {
			return nil, translate(err)
		}
		records = append(records, t)
	}
	return records, translate(rows.Err())
}

// GetTLERecordByID returns one specific orbital data version.
//
// A pass plan is built from the TLE the pass was planned with, so this lookup
// is by identity rather than "the newest one".
func (r *Repository) GetTLERecordByID(ctx context.Context, id uuid.UUID) (domain.TLERecord, error) {
	row := r.db.QueryRow(ctx, `SELECT `+tleColumns+` FROM tle_records WHERE id = $1`, id)
	return scanTLE(row)
}

// LatestTLERecordForEach returns the newest orbital data per satellite, keyed
// by satellite id.
//
// One query rather than one per satellite, because the admin TLE view reads
// the whole catalogue at once. A satellite with no orbital data is simply
// absent from the map.
func (r *Repository) LatestTLERecordForEach(ctx context.Context) (map[uuid.UUID]domain.TLERecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (satellite_id) `+tleColumns+`
		FROM tle_records
		ORDER BY satellite_id, epoch DESC, fetched_at DESC`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	latest := map[uuid.UUID]domain.TLERecord{}
	for rows.Next() {
		var t domain.TLERecord
		if err := rows.Scan(&t.ID, &t.SatelliteID, &t.Line1, &t.Line2, &t.Epoch,
			&t.Source, &t.FetchedAt); err != nil {
			return nil, translate(err)
		}
		latest[t.SatelliteID] = t
	}
	return latest, translate(rows.Err())
}
