-- +goose Up
-- +goose StatementBegin

CREATE TABLE pass_plans (
    pass_id      uuid PRIMARY KEY REFERENCES passes(id) ON DELETE CASCADE,
    generation   text NOT NULL,
    plan_version integer NOT NULL CHECK (plan_version > 0),
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
