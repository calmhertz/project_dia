-- +goose Up
-- +goose StatementBegin

-- The executable instruction generated for an approved pass.
--
-- A plan is deterministic, so this table is not strictly required to rebuild
-- one. It exists so the Server can tell whether a plan actually changed
-- without regenerating it, and so the exact bytes a Worker was given remain
-- inspectable after the fact.
CREATE TABLE pass_plans (
    pass_id      uuid PRIMARY KEY REFERENCES passes(id) ON DELETE CASCADE,
    -- Content hash over the plan excluding its generation timestamp.
    generation   text NOT NULL,
    plan_version integer NOT NULL CHECK (plan_version > 0),
    -- The serialized protobuf handed to the Worker.
    encoded      bytea NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT pass_plans_generation_shape CHECK (generation ~ '^[a-f0-9]{64}$'),
    CONSTRAINT pass_plans_encoded_not_empty CHECK (octet_length(encoded) > 0)
);

CREATE INDEX pass_plans_generation_idx ON pass_plans (generation);

CREATE TRIGGER pass_plans_set_updated_at BEFORE UPDATE ON pass_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pass_plans;
-- +goose StatementEnd
