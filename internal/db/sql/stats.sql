-- name: GetUsageByDay :many
SELECT
    date(created_at, 'unixepoch') as day,
    SUM(prompt_tokens) as prompt_tokens,
    SUM(completion_tokens) as completion_tokens,
    SUM(input_tokens) as input_tokens,
    SUM(cache_read_tokens) as cache_read_tokens,
    SUM(cache_write_tokens) as cache_write_tokens,
    SUM(cost) as cost,
    COUNT(*) as session_count
FROM sessions
WHERE parent_session_id IS NULL
GROUP BY date(created_at, 'unixepoch')
ORDER BY day DESC;

-- name: GetUsageByModel :many
SELECT
    COALESCE(model, 'unknown') as model,
    COALESCE(provider, 'unknown') as provider,
    COUNT(*) as message_count
FROM messages
WHERE role = 'assistant'
GROUP BY model, provider
ORDER BY message_count DESC;

-- name: GetUsageByHour :many
SELECT
    CAST(strftime('%H', created_at, 'unixepoch', 'localtime') AS INTEGER) as hour,
    COUNT(*) as session_count
FROM sessions
WHERE parent_session_id IS NULL
GROUP BY hour
ORDER BY hour;

-- name: GetUsageByDayOfWeek :many
SELECT
    CAST(strftime('%w', created_at, 'unixepoch') AS INTEGER) as day_of_week,
    COUNT(*) as session_count,
    SUM(prompt_tokens) as prompt_tokens,
    SUM(completion_tokens) as completion_tokens,
    SUM(input_tokens) as input_tokens,
    SUM(cache_read_tokens) as cache_read_tokens,
    SUM(cache_write_tokens) as cache_write_tokens
FROM sessions
WHERE parent_session_id IS NULL
GROUP BY day_of_week
ORDER BY day_of_week;

-- name: GetTotalStats :one
SELECT
    COUNT(*) as total_sessions,
    COALESCE(SUM(prompt_tokens), 0) as total_prompt_tokens,
    COALESCE(SUM(completion_tokens), 0) as total_completion_tokens,
    COALESCE(SUM(input_tokens), 0) as total_input_tokens,
    COALESCE(SUM(cache_read_tokens), 0) as total_cache_read_tokens,
    COALESCE(SUM(cache_write_tokens), 0) as total_cache_write_tokens,
    COALESCE(SUM(cost), 0) as total_cost,
    COALESCE(SUM(message_count), 0) as total_messages,
    COALESCE(AVG(prompt_tokens + completion_tokens), 0) as avg_tokens_per_session,
    COALESCE(AVG(message_count), 0) as avg_messages_per_session
FROM sessions
WHERE parent_session_id IS NULL;

-- name: GetRecentActivity :many
SELECT
    date(created_at, 'unixepoch') as day,
    COUNT(*) as session_count,
    SUM(prompt_tokens + completion_tokens) as total_tokens,
    SUM(cost) as cost
FROM sessions
WHERE parent_session_id IS NULL
  AND created_at >= strftime('%s', 'now', '-30 days')
GROUP BY date(created_at, 'unixepoch')
ORDER BY day ASC;

-- name: GetAverageResponseTime :one
SELECT
    CAST(COALESCE(AVG(finished_at - created_at), 0) AS INTEGER) as avg_response_seconds
FROM messages
WHERE role = 'assistant'
  AND finished_at IS NOT NULL
  AND finished_at > created_at;

-- name: GetToolUsage :many
SELECT
    json_extract(value, '$.data.name') as tool_name,
    COUNT(*) as call_count
FROM messages, json_each(parts)
WHERE json_extract(value, '$.type') = 'tool_call'
  AND json_extract(value, '$.data.name') IS NOT NULL
GROUP BY tool_name
ORDER BY call_count DESC;

-- name: GetToolUsageWithTokens :many
SELECT
    tool_name,
    call_count,
    total_chars,
    -- Estimate tokens as chars/4 (rough approximation)
    CAST(total_chars / 4 AS INTEGER) as estimated_tokens
FROM (
    SELECT
        json_extract(value, '$.data.name') as tool_name,
        COUNT(*) as call_count,
        COALESCE(SUM(LENGTH(json_extract(value, '$.data.content'))), 0) as total_chars
    FROM messages, json_each(parts)
    WHERE json_extract(value, '$.type') = 'tool_result'
      AND json_extract(value, '$.data.name') IS NOT NULL
    GROUP BY tool_name
)
ORDER BY estimated_tokens DESC;

-- name: GetHourDayTokenHeatmap :many
SELECT
    CAST(strftime('%w', created_at, 'unixepoch', 'localtime') AS INTEGER) as day_of_week,
    CAST(strftime('%H', created_at, 'unixepoch', 'localtime') AS INTEGER) as hour,
    COALESCE(SUM(prompt_tokens + completion_tokens), 0) as total_tokens,
    COALESCE(SUM(input_tokens + completion_tokens), 0) as total_tokens_excl_cache
FROM sessions
WHERE parent_session_id IS NULL
GROUP BY day_of_week, hour
ORDER BY day_of_week, hour;

-- name: GetHeatmapDateRange :one
SELECT
    date(MIN(created_at), 'unixepoch', 'localtime') as first_day,
    date(MAX(created_at), 'unixepoch', 'localtime') as last_day,
    CAST(julianday(date(MAX(created_at), 'unixepoch', 'localtime')) - julianday(date(MIN(created_at), 'unixepoch', 'localtime')) + 1 AS INTEGER) as total_days
FROM sessions
WHERE parent_session_id IS NULL;
