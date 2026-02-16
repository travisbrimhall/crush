-- name: CreateSessionSummary :one
INSERT INTO session_summaries (
    id,
    session_id,
    summary_text,
    embedding,
    key_topics,
    created_at
) VALUES (
    ?, ?, ?, ?, ?, strftime('%s', 'now')
)
RETURNING *;

-- name: GetSessionSummary :one
SELECT *
FROM session_summaries
WHERE id = ? LIMIT 1;

-- name: GetSessionSummaryBySessionID :one
SELECT *
FROM session_summaries
WHERE session_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: ListSessionSummaries :many
SELECT *
FROM session_summaries
ORDER BY created_at DESC;

-- name: ListSessionSummariesWithEmbeddings :many
SELECT *
FROM session_summaries
WHERE embedding IS NOT NULL
ORDER BY created_at DESC;

-- name: UpdateSessionSummaryEmbedding :exec
UPDATE session_summaries
SET embedding = ?
WHERE id = ?;

-- name: DeleteSessionSummary :exec
DELETE FROM session_summaries
WHERE id = ?;

-- name: DeleteSessionSummariesBySessionID :exec
DELETE FROM session_summaries
WHERE session_id = ?;
