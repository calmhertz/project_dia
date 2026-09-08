-- +goose Up
-- +goose StatementBegin

-- Two protections collided and between them made every account undeletable.
--
-- audit_records.actor_user_id was declared ON DELETE SET NULL, so removing a
-- user made PostgreSQL UPDATE their audit rows. The audit trail is
-- append-only, so its trigger refused the update, and the delete failed with
-- "audit_records is append-only". Every login is audited, so in practice no
-- account that had ever signed in could be removed.
--
-- Nulling the actor was the wrong behaviour anyway: an audit trail exists to
-- say who did something, and erasing the actor to tidy up after a deleted
-- account destroys exactly the evidence the trail is for (spec.md section
-- 17.3). The identifier stays, and a reader who cannot resolve it to a
-- current account learns something true: the account is gone.
--
-- The column keeps its type and index; only the foreign key goes.
ALTER TABLE audit_records DROP CONSTRAINT audit_records_actor_user_id_fkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restoring the constraint requires that every recorded actor still exists.
DELETE FROM audit_records
WHERE actor_user_id IS NOT NULL
  AND actor_user_id NOT IN (SELECT id FROM users);

ALTER TABLE audit_records
    ADD CONSTRAINT audit_records_actor_user_id_fkey
    FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL;

-- +goose StatementEnd
