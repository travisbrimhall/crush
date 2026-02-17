-- +goose Up
ALTER TABLE sessions ADD COLUMN template_id TEXT;

-- +goose Down
ALTER TABLE sessions DROP COLUMN template_id;
