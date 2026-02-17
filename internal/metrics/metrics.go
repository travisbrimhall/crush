package metrics

import (
	"context"
	"database/sql"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/db"
)

// Service records tool execution metrics.
type Service interface {
	Record(ctx context.Context, metric ToolMetric) error
	WrapTool(tool fantasy.AgentTool) fantasy.AgentTool
}

// ToolMetric represents a single tool execution measurement.
type ToolMetric struct {
	SessionID    string
	ToolName     string
	StartedAt    time.Time
	Duration     time.Duration
	Success      bool
	ErrorMessage string
	InputSize    int
	OutputSize   int
}

type service struct {
	q *db.Queries
}

// New creates a new metrics service.
func New(q *db.Queries) Service {
	return &service{q: q}
}

// Record saves a tool metric to the database.
func (s *service) Record(ctx context.Context, metric ToolMetric) error {
	var errMsg sql.NullString
	if metric.ErrorMessage != "" {
		errMsg = sql.NullString{String: metric.ErrorMessage, Valid: true}
	}

	success := int64(0)
	if metric.Success {
		success = 1
	}

	return s.q.RecordToolMetric(ctx, db.RecordToolMetricParams{
		SessionID:    metric.SessionID,
		ToolName:     metric.ToolName,
		StartedAt:    metric.StartedAt.Unix(),
		DurationMs:   metric.Duration.Milliseconds(),
		Success:      success,
		ErrorMessage: errMsg,
		InputSize:    sql.NullInt64{Int64: int64(metric.InputSize), Valid: metric.InputSize > 0},
		OutputSize:   sql.NullInt64{Int64: int64(metric.OutputSize), Valid: metric.OutputSize > 0},
	})
}

// WrapTool wraps a tool to record execution metrics.
func (s *service) WrapTool(tool fantasy.AgentTool) fantasy.AgentTool {
	return &wrappedTool{
		inner:   tool,
		service: s,
	}
}

// wrappedTool implements fantasy.AgentTool and records metrics on execution.
type wrappedTool struct {
	inner   fantasy.AgentTool
	service *service
}

func (w *wrappedTool) Info() fantasy.ToolInfo {
	return w.inner.Info()
}

func (w *wrappedTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	startedAt := time.Now()

	resp, err := w.inner.Run(ctx, params)

	duration := time.Since(startedAt)

	// Extract session ID from context.
	sessionID := tools.GetSessionFromContext(ctx)
	if sessionID == "" {
		// No session, skip recording.
		return resp, err
	}

	// Calculate sizes.
	inputSize := len(params.Input)
	outputSize := len(resp.Content)

	// Build metric.
	metric := ToolMetric{
		SessionID:  sessionID,
		ToolName:   w.inner.Info().Name,
		StartedAt:  startedAt,
		Duration:   duration,
		Success:    err == nil && !resp.IsError,
		InputSize:  inputSize,
		OutputSize: outputSize,
	}

	if err != nil {
		metric.ErrorMessage = err.Error()
	} else if resp.IsError {
		metric.ErrorMessage = resp.Content
	}

	// Record async to avoid blocking tool execution.
	go func() {
		_ = w.service.Record(context.Background(), metric)
	}()

	return resp, err
}

func (w *wrappedTool) ProviderOptions() fantasy.ProviderOptions {
	return w.inner.ProviderOptions()
}

func (w *wrappedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	w.inner.SetProviderOptions(opts)
}
