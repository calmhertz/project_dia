-- +goose Up
-- +goose StatementBegin

ALTER TABLE audit_records DROP CONSTRAINT audit_records_actor_user_id_fkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM audit_records
WHERE actor_user_id IS NOT NULL
  AND actor_user_id NOT IN (SELECT id FROM users);

ALTER TABLE audit_records
    ADD CONSTRAINT audit_records_actor_user_id_fkey
    FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL;

-- +goose StatementEnd
