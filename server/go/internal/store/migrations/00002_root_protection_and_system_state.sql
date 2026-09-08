-- +goose Up
-- +goose StatementBegin

-- spec.md section 17.1: Root cannot be deleted and cannot be demoted. This is
-- enforced in the database so no API path, CLI or manual query can bypass it.
CREATE FUNCTION protect_root_user() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.role = 'root' THEN
            RAISE EXCEPTION 'root user cannot be deleted'
                USING ERRCODE = 'restrict_violation';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.role = 'root' AND NEW.role <> 'root' THEN
        RAISE EXCEPTION 'root user cannot be demoted'
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER users_protect_root
    BEFORE UPDATE OR DELETE ON users
    FOR EACH ROW EXECUTE FUNCTION protect_root_user();
-- +goose StatementEnd

-- +goose StatementBegin
-- Single-row marker for first-run completion (spec.md section 23).
CREATE TABLE system_state (
    id             boolean PRIMARY KEY DEFAULT true,
    initialized_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT system_state_is_singleton CHECK (id)
);

INSERT INTO system_state (id) VALUES (true);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER system_state_set_updated_at BEFORE UPDATE ON system_state
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS system_state;
DROP TRIGGER IF EXISTS users_protect_root ON users;
DROP FUNCTION IF EXISTS protect_root_user();
-- +goose StatementEnd
