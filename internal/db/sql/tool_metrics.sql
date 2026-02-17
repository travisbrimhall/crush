-- name: RecordToolMetric :exec
INSERT INTO tool_metrics (
    session_id,
    tool_name,
    started_at,
    duration_ms,
    success,
    error_message,
    input_size,
    output_size
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?
);

-- name: GetSessionToolMetrics :many
SELECT * FROM tool_metrics
WHERE session_id = ?
ORDER BY started_at DESC;

-- name: GetToolMetricsSummary :many
SELECT
    tool_name,
    COUNT(*) as call_count,
    AVG(duration_ms) as avg_duration_ms,
    MIN(duration_ms) as min_duration_ms,
    MAX(duration_ms) as max_duration_ms,
    SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as success_count,
    SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END) as failure_count
FROM tool_metrics
WHERE session_id = ?
GROUP BY tool_name
ORDER BY call_count DESC;

-- name: GetAllToolMetricsSummary :many
SELECT
    tool_name,
    COUNT(*) as call_count,
    AVG(duration_ms) as avg_duration_ms,
    MIN(duration_ms) as min_duration_ms,
    MAX(duration_ms) as max_duration_ms,
    SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as success_count,
    SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END) as failure_count
FROM tool_metrics
GROUP BY tool_name
ORDER BY call_count DESC;

-- name: GetSlowestTools :many
SELECT * FROM tool_metrics
WHERE session_id = ?
ORDER BY duration_ms DESC
LIMIT ?;

-- name: DeleteSessionToolMetrics :exec
DELETE FROM tool_metrics WHERE session_id = ?;
