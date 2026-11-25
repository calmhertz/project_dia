-- +goose Up
-- +goose StatementBegin

CREATE TYPE recording_status AS ENUM ('stored', 'failed');

CREATE TABLE recordings (
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
