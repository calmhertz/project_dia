-- +goose Up
-- +goose StatementBegin

-- btree_gist lets the station overlap constraint mix equality and range operators.
CREATE EXTENSION IF NOT EXISTS btree_gist;
-- citext gives case-insensitive usernames without per-query lower().
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TYPE user_role AS ENUM ('root', 'admin', 'user');
CREATE TYPE rf_band AS ENUM ('vhf', 'uhf');
CREATE TYPE worker_connection_state AS ENUM ('online', 'offline');
CREATE TYPE tle_source AS ENUM ('celestrak', 'satnogs', 'manual');
CREATE TYPE recording_mode AS ENUM ('raw', 'process', 'raw_and_process');
CREATE TYPE pass_visibility AS ENUM ('private', 'public');
CREATE TYPE pass_status AS ENUM (
    'pending_approval', 'approved', 'rejected', 'cancelled',
    'executing', 'completed', 'failed', 'missed'
);
CREATE TYPE pass_cancellation_reason AS ENUM (
    'cancelled_by_owner', 'cancelled_by_admin', 'cancelled_by_root_override'
);

CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Users -----------------------------------------------------------------

CREATE TABLE users (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username             citext NOT NULL UNIQUE,
    password_hash        text NOT NULL,
    role                 user_role NOT NULL DEFAULT 'user',
    must_change_password boolean NOT NULL DEFAULT true,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_username_not_blank CHECK (length(trim(username::text)) > 0),
    -- spec.md section 21: passwords are Argon2id, enforced at the boundary the
    -- database can see so no code path can persist another format.
    CONSTRAINT users_password_hash_is_argon2id CHECK (password_hash LIKE '$argon2id$%')
);

-- spec.md section 17.1: exactly one Root exists and cannot be duplicated.
CREATE UNIQUE INDEX users_single_root ON users ((role)) WHERE role = 'root';

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Stations --------------------------------------------------------------

CREATE TABLE stations (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text NOT NULL UNIQUE,
    latitude       double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude      double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    altitude_m     double precision NOT NULL,
    timezone       text NOT NULL DEFAULT 'UTC',
    -- Only one RF chain is active because there is a single RTL-SDR.
    active_rf_band rf_band NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER stations_set_updated_at BEFORE UPDATE ON stations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE station_band_configs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    station_id          uuid NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
    band                rf_band NOT NULL,
    antenna_description text NOT NULL DEFAULT '',
    center_frequency_hz bigint CHECK (center_frequency_hz > 0),
    sample_rate_hz      integer CHECK (sample_rate_hz > 0),
    gain_db             numeric(6, 2),
    ppm_correction      integer NOT NULL DEFAULT 0,
    bias_tee_enabled    boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    UNIQUE (station_id, band)
);

CREATE TRIGGER station_band_configs_set_updated_at BEFORE UPDATE ON station_band_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE station_hardware_configs (
    station_id                     uuid PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
    rotator_serial_port            text NOT NULL,
    rotator_baud_rate              integer NOT NULL CHECK (rotator_baud_rate > 0),
    -- spec.md section 9: the G-550 accepts azimuth 0..359 and elevation 0..90.
    rotator_park_azimuth_degrees   integer NOT NULL DEFAULT 0
        CHECK (rotator_park_azimuth_degrees BETWEEN 0 AND 359),
    rotator_park_elevation_degrees integer NOT NULL DEFAULT 0
        CHECK (rotator_park_elevation_degrees BETWEEN 0 AND 90),
    sdr_device_identifier          text NOT NULL DEFAULT '',
    created_at                     timestamptz NOT NULL DEFAULT now(),
    updated_at                     timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER station_hardware_configs_set_updated_at BEFORE UPDATE ON station_hardware_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Scheduling configuration is versioned rather than updated in place: spec.md
-- section 13.6 requires a delay before a newly chosen lead time may be used,
-- and section 13.8 requires existing approved passes to keep their own terms.
CREATE TABLE scheduling_configs (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    station_id                uuid NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
    minimum_lead_time         interval NOT NULL CHECK (minimum_lead_time >= interval '0'),
    pre_pass_buffer           interval NOT NULL CHECK (pre_pass_buffer >= interval '0'),
    post_pass_buffer          interval NOT NULL CHECK (post_pass_buffer >= interval '0'),
    recording_pre_roll        interval NOT NULL CHECK (recording_pre_roll >= interval '0'),
    recording_post_roll       interval NOT NULL CHECK (recording_post_roll >= interval '0'),
    minimum_elevation_degrees numeric(4, 1) NOT NULL
        CHECK (minimum_elevation_degrees BETWEEN 0 AND 90),
    created_by                uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at                timestamptz NOT NULL DEFAULT now(),
    -- The moment this configuration may start being used for new scheduling.
    -- spec.md section 13.6 requires a delay derived from the *previous*
    -- configuration's lead time, which no row-level check can see, so the
    -- Server computes and enforces this value in V7.
    effective_from            timestamptz NOT NULL
);

CREATE INDEX scheduling_configs_station_effective_idx
    ON scheduling_configs (station_id, effective_from DESC);

-- Workers ---------------------------------------------------------------

CREATE TABLE workers (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    station_id               uuid NOT NULL REFERENCES stations(id) ON DELETE RESTRICT,
    name                     text NOT NULL UNIQUE,
    worker_version           text NOT NULL DEFAULT '',
    connection_state         worker_connection_state NOT NULL DEFAULT 'offline',
    last_seen_at             timestamptz,
    last_sync_at             timestamptz,
    -- Latest-state reconciliation marker (spec.md section 6.2).
    desired_state_generation bigint NOT NULL DEFAULT 0
        CHECK (desired_state_generation >= 0),
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workers_station_idx ON workers (station_id);

CREATE TRIGGER workers_set_updated_at BEFORE UPDATE ON workers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Satellites and orbital data -------------------------------------------

CREATE TABLE satellites (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- spec.md section 11.1: NORAD id is the stable identity, not the TLE text.
    norad_id       integer NOT NULL UNIQUE CHECK (norad_id > 0),
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    -- Enrichment only; missing metadata must never block tracking.
    metadata       jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_schedulable boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER satellites_set_updated_at BEFORE UPDATE ON satellites
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE tle_records (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    satellite_id uuid NOT NULL REFERENCES satellites(id) ON DELETE CASCADE,
    line1        text NOT NULL,
    line2        text NOT NULL,
    epoch        timestamptz NOT NULL,
    source       tle_source NOT NULL,
    fetched_at   timestamptz NOT NULL DEFAULT now(),

    -- Trailing spaces are sometimes trimmed by providers, so 68 is tolerated.
    CONSTRAINT tle_records_line1_shape
        CHECK (line1 LIKE '1 %' AND char_length(line1) BETWEEN 68 AND 69),
    CONSTRAINT tle_records_line2_shape
        CHECK (line2 LIKE '2 %' AND char_length(line2) BETWEEN 68 AND 69),
    -- Re-fetching an unchanged TLE must not create a second row.
    UNIQUE (satellite_id, line1, line2)
);

CREATE INDEX tle_records_satellite_epoch_idx
    ON tle_records (satellite_id, epoch DESC);

-- SatDump pipelines -----------------------------------------------------

CREATE TABLE satdump_pipelines (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL UNIQUE,
    is_custom       boolean NOT NULL DEFAULT true,
    -- Custom pipeline JSON lives on disk; only the reference is relational.
    definition_path text,
    checksum_sha256 text CHECK (checksum_sha256 IS NULL OR checksum_sha256 ~ '^[a-f0-9]{64}$'),
    uploaded_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT satdump_pipelines_custom_has_definition
        CHECK (NOT is_custom OR definition_path IS NOT NULL)
);

-- Passes ----------------------------------------------------------------

CREATE TABLE passes (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    station_id            uuid NOT NULL REFERENCES stations(id) ON DELETE RESTRICT,
    satellite_id          uuid NOT NULL REFERENCES satellites(id) ON DELETE RESTRICT,
    requested_by          uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    -- The TLE the plan was built from, so a historical pass stays reproducible
    -- after the satellite's orbital data is refreshed (server-spec section 16).
    tle_record_id         uuid NOT NULL REFERENCES tle_records(id) ON DELETE RESTRICT,
    scheduling_config_id  uuid NOT NULL REFERENCES scheduling_configs(id) ON DELETE RESTRICT,

    status                pass_status NOT NULL DEFAULT 'pending_approval',
    visibility            pass_visibility NOT NULL DEFAULT 'private',
    band                  rf_band NOT NULL,

    aos_at                timestamptz NOT NULL,
    los_at                timestamptz NOT NULL,
    tca_at                timestamptz,
    max_elevation_degrees numeric(4, 1) NOT NULL
        CHECK (max_elevation_degrees BETWEEN 0 AND 90),

    -- Resource reservation = AOS/LOS widened by the station buffers
    -- (spec.md section 13.4). Distinct from the recording margins below.
    reserved_from         timestamptz NOT NULL,
    reserved_to           timestamptz NOT NULL,
    reservation           tstzrange GENERATED ALWAYS AS
        (tstzrange(reserved_from, reserved_to, '[)')) STORED,

    recording_mode        recording_mode NOT NULL,
    recording_pre_roll    interval NOT NULL CHECK (recording_pre_roll >= interval '0'),
    recording_post_roll   interval NOT NULL CHECK (recording_post_roll >= interval '0'),
    pipeline_id           uuid REFERENCES satdump_pipelines(id) ON DELETE RESTRICT,
    radio_settings        jsonb NOT NULL DEFAULT '{}'::jsonb,

    approved_by           uuid REFERENCES users(id) ON DELETE SET NULL,
    approved_at           timestamptz,
    cancellation_reason   pass_cancellation_reason,
    cancelled_by          uuid REFERENCES users(id) ON DELETE SET NULL,
    cancelled_at          timestamptz,

    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT passes_los_after_aos CHECK (los_at > aos_at),
    CONSTRAINT passes_reservation_covers_pass
        CHECK (reserved_from <= aos_at AND reserved_to >= los_at),
    CONSTRAINT passes_cancellation_fields_agree
        CHECK ((status = 'cancelled') = (cancellation_reason IS NOT NULL)),

    -- spec.md section 13.3: the station is a single resource, so any overlap on
    -- the same station conflicts regardless of satellite, band or user. Only
    -- live states hold the resource; terminal states stay for history.
    CONSTRAINT passes_no_station_overlap EXCLUDE USING gist (
        station_id WITH =,
        reservation WITH &&
    ) WHERE (status IN ('pending_approval', 'approved', 'executing'))
);

CREATE INDEX passes_station_reserved_from_idx ON passes (station_id, reserved_from);
CREATE INDEX passes_requested_by_idx ON passes (requested_by);
CREATE INDEX passes_satellite_idx ON passes (satellite_id);
CREATE INDEX passes_status_idx ON passes (status);
CREATE INDEX passes_public_history_idx ON passes (los_at DESC)
    WHERE visibility = 'public';

CREATE TRIGGER passes_set_updated_at BEFORE UPDATE ON passes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Audit -----------------------------------------------------------------

CREATE TABLE audit_records (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Null actor means the action was taken by the system itself.
    actor_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    action         text NOT NULL,
    entity_type    text NOT NULL,
    entity_id      text,
    previous_state jsonb,
    new_state      jsonb,
    reason         text,
    created_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_records_action_not_blank CHECK (length(trim(action)) > 0)
);

CREATE INDEX audit_records_created_at_idx ON audit_records (created_at DESC);
CREATE INDEX audit_records_entity_idx ON audit_records (entity_type, entity_id);
CREATE INDEX audit_records_actor_idx ON audit_records (actor_user_id);

-- +goose StatementEnd

-- +goose StatementBegin
-- The audit trail is append-only: rewriting history would defeat its purpose.
CREATE FUNCTION audit_records_are_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_records is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_records_no_update_or_delete
    BEFORE UPDATE OR DELETE ON audit_records
    FOR EACH ROW EXECUTE FUNCTION audit_records_are_append_only();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_records;
DROP FUNCTION IF EXISTS audit_records_are_append_only();
DROP TABLE IF EXISTS passes;
DROP TABLE IF EXISTS satdump_pipelines;
DROP TABLE IF EXISTS tle_records;
DROP TABLE IF EXISTS satellites;
DROP TABLE IF EXISTS workers;
DROP TABLE IF EXISTS scheduling_configs;
DROP TABLE IF EXISTS station_hardware_configs;
DROP TABLE IF EXISTS station_band_configs;
DROP TABLE IF EXISTS stations;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TYPE IF EXISTS pass_cancellation_reason;
DROP TYPE IF EXISTS pass_status;
DROP TYPE IF EXISTS pass_visibility;
DROP TYPE IF EXISTS recording_mode;
DROP TYPE IF EXISTS tle_source;
DROP TYPE IF EXISTS worker_connection_state;
DROP TYPE IF EXISTS rf_band;
DROP TYPE IF EXISTS user_role;
-- +goose StatementEnd
