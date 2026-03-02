-- +goose Up
-- Add separate tracking for cache tokens and fresh input tokens.
-- prompt_tokens retains legacy semantics (input + cache_read combined).
-- input_tokens tracks only fresh, non-cached input tokens.
ALTER TABLE sessions ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0);
ALTER TABLE sessions ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0);
ALTER TABLE sessions ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0);

-- +goose Down
ALTER TABLE sessions DROP COLUMN input_tokens;
ALTER TABLE sessions DROP COLUMN cache_read_tokens;
ALTER TABLE sessions DROP COLUMN cache_write_tokens;
