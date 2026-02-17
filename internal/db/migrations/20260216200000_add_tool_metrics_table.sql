-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tool_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL CHECK (session_id != ''),
    tool_name TEXT NOT NULL CHECK (tool_name != ''),
    started_at INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    success INTEGER NOT NULL DEFAULT 1,
    error_message TEXT,
    input_size INTEGER,
    output_size INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tool_metrics_session_id ON tool_metrics (session_id);
CREATE INDEX IF NOT EXISTS idx_tool_metrics_tool_name ON tool_metrics (tool_name);
CREATE INDEX IF NOT EXISTS idx_tool_metrics_started_at ON tool_metrics (started_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tool_metrics_started_at;
DROP INDEX IF EXISTS idx_tool_metrics_tool_name;
DROP INDEX IF EXISTS idx_tool_metrics_session_id;
DROP TABLE IF EXISTS tool_metrics;
-- +goose StatementEnd
