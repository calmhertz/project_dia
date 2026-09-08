package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"aagasa/internal/domain"
)

// ErrRootProtected is returned when a caller tries to delete or demote Root.
// The database trigger is the authority; this maps it to a typed error.
var ErrRootProtected = errors.New("root user cannot be deleted or demoted")

// restrictViolation is the SQLSTATE the root-protection trigger raises.
const restrictViolation = "23001"

// foreignKeyViolation is what a RESTRICT reference raises, which is a
// different refusal with a different answer.
const foreignKeyViolation = "23503"

func translateUser(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == restrictViolation {
		return ErrRootProtected
	}
	return translate(err)
}

// CountUsers reports how many accounts exist. Zero means the system has not
// been bootstrapped.
func (r *Repository) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count)
	return count, translate(err)
}

// ListUsers returns all accounts ordered by creation.
func (r *Repository) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := r.db.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role,
			&user.MustChangePassword, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		users = append(users, user)
	}
	return users, translate(rows.Err())
}

// SetPassword stores a new hash and clears the forced-change flag.
func (r *Repository) SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE users SET password_hash = $2, must_change_password = false
		WHERE id = $1`, id, passwordHash)
	if err != nil {
		return translateUser(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserRole changes a user's role. Demoting Root is rejected by the database.
func (r *Repository) SetUserRole(ctx context.Context, id uuid.UUID, role domain.UserRole) error {
	tag, err := r.db.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, role)
	if err != nil {
		return translateUser(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes an account. Deleting Root is rejected by the database.
func (r *Repository) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return translateUser(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// System state ------------------------------------------------------------

// SystemInitializedAt reports when first-run setup completed, or nil if it has
// not (spec.md section 23).
func (r *Repository) SystemInitializedAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	err := r.db.QueryRow(ctx, `SELECT initialized_at FROM system_state WHERE id = true`).Scan(&at)
	return at, translate(err)
}

// MarkSystemInitialized records first-run completion. Calling it again is a
// no-op so setup cannot be silently re-run.
func (r *Repository) MarkSystemInitialized(ctx context.Context, at time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE system_state SET initialized_at = $1
		WHERE id = true AND initialized_at IS NULL`, at)
	return translate(err)
}

// Station configuration ---------------------------------------------------

// UpsertStationBandConfig stores the antenna and radio settings for one band.
func (r *Repository) UpsertStationBandConfig(ctx context.Context, cfg domain.StationBandConfig) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO station_band_configs (station_id, band, antenna_description,
			center_frequency_hz, sample_rate_hz, gain_db, ppm_correction, bias_tee_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (station_id, band) DO UPDATE SET
			antenna_description = EXCLUDED.antenna_description,
			center_frequency_hz = EXCLUDED.center_frequency_hz,
			sample_rate_hz      = EXCLUDED.sample_rate_hz,
			gain_db             = EXCLUDED.gain_db,
			ppm_correction      = EXCLUDED.ppm_correction,
			bias_tee_enabled    = EXCLUDED.bias_tee_enabled`,
		cfg.StationID, cfg.Band, cfg.AntennaDescription, cfg.CenterFrequencyHz,
		cfg.SampleRateHz, cfg.GainDB, cfg.PPMCorrection, cfg.BiasTeeEnabled)
	return translate(err)
}

// GetStationBandConfigs returns every band configuration for a station.
func (r *Repository) GetStationBandConfigs(ctx context.Context, stationID uuid.UUID) ([]domain.StationBandConfig, error) {
	rows, err := r.db.Query(ctx, `
		SELECT station_id, band, antenna_description, center_frequency_hz,
			sample_rate_hz, gain_db, ppm_correction, bias_tee_enabled
		FROM station_band_configs WHERE station_id = $1 ORDER BY band`, stationID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var configs []domain.StationBandConfig
	for rows.Next() {
		var cfg domain.StationBandConfig
		if err := rows.Scan(&cfg.StationID, &cfg.Band, &cfg.AntennaDescription,
			&cfg.CenterFrequencyHz, &cfg.SampleRateHz, &cfg.GainDB,
			&cfg.PPMCorrection, &cfg.BiasTeeEnabled); err != nil {
			return nil, translate(err)
		}
		configs = append(configs, cfg)
	}
	return configs, translate(rows.Err())
}

// UpsertStationHardwareConfig stores the rotator and SDR settings.
func (r *Repository) UpsertStationHardwareConfig(ctx context.Context, cfg domain.StationHardwareConfig) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO station_hardware_configs (station_id, rotator_serial_port,
			rotator_baud_rate, rotator_park_azimuth_degrees,
			rotator_park_elevation_degrees, sdr_device_identifier)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (station_id) DO UPDATE SET
			rotator_serial_port            = EXCLUDED.rotator_serial_port,
			rotator_baud_rate              = EXCLUDED.rotator_baud_rate,
			rotator_park_azimuth_degrees   = EXCLUDED.rotator_park_azimuth_degrees,
			rotator_park_elevation_degrees = EXCLUDED.rotator_park_elevation_degrees,
			sdr_device_identifier          = EXCLUDED.sdr_device_identifier`,
		cfg.StationID, cfg.RotatorSerialPort, cfg.RotatorBaudRate,
		cfg.RotatorParkAzimuthDegrees, cfg.RotatorParkElevationDegrees,
		cfg.SDRDeviceIdentifier)
	return translate(err)
}

// GetStationHardwareConfig returns the rotator and SDR settings.
func (r *Repository) GetStationHardwareConfig(ctx context.Context, stationID uuid.UUID) (domain.StationHardwareConfig, error) {
	var cfg domain.StationHardwareConfig
	err := r.db.QueryRow(ctx, `
		SELECT station_id, rotator_serial_port, rotator_baud_rate,
			rotator_park_azimuth_degrees, rotator_park_elevation_degrees,
			sdr_device_identifier
		FROM station_hardware_configs WHERE station_id = $1`, stationID).Scan(
		&cfg.StationID, &cfg.RotatorSerialPort, &cfg.RotatorBaudRate,
		&cfg.RotatorParkAzimuthDegrees, &cfg.RotatorParkElevationDegrees,
		&cfg.SDRDeviceIdentifier)
	return cfg, translate(err)
}

// ListStations returns every station.
func (r *Repository) ListStations(ctx context.Context) ([]domain.Station, error) {
	rows, err := r.db.Query(ctx, `SELECT `+stationColumns+` FROM stations ORDER BY created_at`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var stations []domain.Station
	for rows.Next() {
		var s domain.Station
		if err := rows.Scan(&s.ID, &s.Name, &s.Latitude, &s.Longitude, &s.AltitudeM,
			&s.Timezone, &s.ActiveRFBand, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		stations = append(stations, s)
	}
	return stations, translate(rows.Err())
}

// SetStationActiveBand switches the station between VHF and UHF. Existing
// approved passes are untouched (spec.md section 13.8).
func (r *Repository) SetStationActiveBand(ctx context.Context, stationID uuid.UUID, band domain.RFBand) error {
	tag, err := r.db.Exec(ctx, `UPDATE stations SET active_rf_band = $2 WHERE id = $1`, stationID, band)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStation changes station identity and location.
//
// The active band is deliberately not settable here: switching bands is its
// own decision with its own consequences, and has SetStationActiveBand.
func (r *Repository) UpdateStation(ctx context.Context, station domain.Station) (domain.Station, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE stations
		SET name = $2, latitude = $3, longitude = $4, altitude_m = $5,
			timezone = $6, updated_at = now()
		WHERE id = $1
		RETURNING `+stationColumns,
		station.ID, station.Name, station.Latitude, station.Longitude,
		station.AltitudeM, station.Timezone)

	var updated domain.Station
	err := row.Scan(&updated.ID, &updated.Name, &updated.Latitude, &updated.Longitude,
		&updated.AltitudeM, &updated.Timezone, &updated.ActiveRFBand,
		&updated.CreatedAt, &updated.UpdatedAt)
	return updated, translate(err)
}
