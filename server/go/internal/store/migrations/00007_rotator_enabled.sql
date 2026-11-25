-- +goose Up
-- +goose StatementBegin

-- A station can run a fixed antenna (monopole, dipole, QFH) with no rotator.
-- Existing stations keep their rotator.
ALTER TABLE station_hardware_configs
    ADD COLUMN rotator_enabled boolean NOT NULL DEFAULT true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE station_hardware_configs DROP COLUMN IF EXISTS rotator_enabled;
-- +goose StatementEnd
