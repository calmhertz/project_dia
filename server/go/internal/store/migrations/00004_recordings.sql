-- +goose Up
-- +goose StatementBegin

CREATE TYPE recording_status AS ENUM ('stored', 'failed');

-- Authoritative recording metadata. Content lives on the filesystem; only the
-- metadata is relational (spec.md section 4.3).
CREATE TABLE recordings (
    -- Deterministic identity computed by the Worker. Being the primary key is
    -- what makes a retried upload idempotent (spec.md section 19.3).
    recording_id    text PRIMARY KEY,
    pass_id         uuid NOT NULL REFERENCES passes(id) ON DELETE RESTRICT,
    worker_id       uuid REFERENCES workers(id) ON DELETE SET NULL,
    station_id      uuid NOT NULL REFERENCES stations(id) ON DELETE RESTRICT,

    relative_path   text NOT NULL,
    stored_path     text NOT NULL,
    size_bytes      bigint NOT NULL CHECK (size_bytes >= 0),
    checksum_sha256 text NOT NULL,

    status          recording_status NOT NULL DEFAULT 'stored',
    started_at      timestamptz,
    finished_at     timestamptz,
    received_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recordings_id_shape CHECK (recording_id ~ '^[a-f0-9]{64}$'),
    CONSTRAINT recordings_checksum_shape CHECK (checksum_sha256 ~ '^[a-f0-9]{64}$'),
    -- One stored file per path within a pass, so two different ids cannot
    -- claim the same content location.
    UNIQUE (pass_id, relative_path)
);

CREATE INDEX recordings_pass_idx ON recordings (pass_id);
CREATE INDEX recordings_station_received_idx ON recordings (station_id, received_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS recordings;
DROP TYPE IF EXISTS recording_status;
-- +goose StatementEnd
