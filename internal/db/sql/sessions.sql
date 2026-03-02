-- name: CreateSession :one
INSERT INTO sessions (
    id,
    parent_session_id,
    title,
    template_id,
    message_count,
    prompt_tokens,
    completion_tokens,
    cost,
    summary_message_id,
    updated_at,
    created_at
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    null,
    strftime('%s', 'now'),
    strftime('%s', 'now')
) RETURNING *;

-- name: GetSessionByID :one
SELECT *
FROM sessions
WHERE id = ? LIMIT 1;

-- name: ListSessions :many
SELECT *
FROM sessions
WHERE parent_session_id is NULL
ORDER BY updated_at DESC;

-- name: UpdateSession :one
UPDATE sessions
SET
    title = ?,
    prompt_tokens = ?,
    completion_tokens = ?,
    input_tokens = ?,
    cache_read_tokens = ?,
    cache_write_tokens = ?,
    summary_message_id = ?,
    cost = ?,
    todos = ?
WHERE id = ?
RETURNING *;

-- name: UpdateSessionTitleAndUsage :exec
UPDATE sessions
SET
    title = ?,
    prompt_tokens = prompt_tokens + ?,
    completion_tokens = completion_tokens + ?,
    input_tokens = input_tokens + ?,
    cache_read_tokens = cache_read_tokens + ?,
    cache_write_tokens = cache_write_tokens + ?,
    cost = cost + ?
WHERE id = ?;


-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;
