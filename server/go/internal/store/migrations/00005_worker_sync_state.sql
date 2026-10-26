-- +goose Up
-- +goose StatementBegin

ALTER TABLE workers
    ADD COLUMN pending_uploads integer NOT NULL DEFAULT 0
        CHECK (pending_uploads >= 0),
    ADD COLUMN pending_reports integer NOT NULL DEFAULT 0
        CHECK (pending_reports >= 0),
    ADD COLUMN synced_generation text NOT NULL DEFAULT '';

CREATE INDEX workers_connection_state_idx ON workers (connection_state, last_seen_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS workers_connection_state_idx;
ALTER TABLE workers
    DROP COLUMN IF EXISTS pending_uploads,
    DROP COLUMN IF EXISTS pending_reports,
    DROP COLUMN IF EXISTS synced_generation;
-- +goose StatementEnd
