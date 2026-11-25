-- +goose Up
-- +goose StatementBegin

-- Per-user notifications, written in the same statement batch as the audit
-- record that caused them. Clients poll this table: the web app while it is
-- open, the Android app from a background job.
CREATE TABLE notifications (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    audit_record_id bigint,
    kind            text NOT NULL,
    severity        text NOT NULL DEFAULT 'info'
                    CHECK (severity IN ('info', 'success', 'warning', 'error')),
    title           text NOT NULL,
    body            text NOT NULL DEFAULT '',
    entity_type     text NOT NULL,
    entity_id       text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    read_at         timestamptz
);

CREATE INDEX notifications_user_idx ON notifications (user_id, id DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
