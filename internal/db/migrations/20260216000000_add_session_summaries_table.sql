-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_summaries (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    summary_text TEXT NOT NULL,
    embedding BLOB,  -- Vector embedding for semantic search
    key_topics TEXT,  -- JSON array of extracted keywords
    created_at INTEGER NOT NULL,  -- Unix timestamp in seconds
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_session_summaries_session_id ON session_summaries (session_id);
CREATE INDEX IF NOT EXISTS idx_session_summaries_created_at ON session_summaries (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_session_summaries_created_at;
DROP INDEX IF EXISTS idx_session_summaries_session_id;
DROP TABLE IF EXISTS session_summaries;
-- +goose StatementEnd
