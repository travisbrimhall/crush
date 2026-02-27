package metrics

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Service records tool execution metrics.
type Service interface {
	// Legacy SQLite methods (deprecated, kept for backward compatibility).
	Record(ctx context.Context, metric ToolMetric) error
	WrapTool(tool fantasy.AgentTool) fantasy.AgentTool

	// Prometheus collector access.
	Collector() *Collector
	Registry() *prometheus.Registry

	// LLM tracking.
	ObserveLLMRequest(provider, model string, duration time.Duration)
	ObserveTimeToFirstToken(provider, model string, duration time.Duration)
	AddTokens(provider, model, tokenType string, count int64)
	IncLLMRequest(provider, model, status string)

	// Tool tracking.
	ObserveTool(tool string, duration time.Duration, inputSize, outputSize int)
	IncToolCall(tool, status string)

	// Agent loop tracking.
	IncAgentStep()
	IncAgentRun(status string)
	IncAgentRetry(provider, reason string)
	IncSummarization()
	IncLoopDetection()
	SetQueueDepth(sessionID string, depth int)

	// Provider error tracking.
	IncProviderError(provider string, statusCode int, errorType string)
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
	q         *db.Queries
	collector *Collector
	registry  *prometheus.Registry
}

// Options configures the metrics service.
type Options struct {
	DB       *db.Queries
	Registry *prometheus.Registry // If nil, uses prometheus.DefaultRegisterer.
}

// New creates a new metrics service with Prometheus support.
func New(q *db.Queries) Service {
	return NewWithOptions(Options{DB: q})
}

// NewWithOptions creates a new metrics service with custom options.
func NewWithOptions(opts Options) Service {
	registry := opts.Registry
	if registry == nil {
		registry = prometheus.NewRegistry()
		// Register default Go metrics.
		registry.MustRegister(collectors.NewGoCollector())
		registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}

	collector := NewCollector(registry)

	return &service{
		q:         opts.DB,
		collector: collector,
		registry:  registry,
	}
}

// Registry returns the Prometheus registry for this service.
func (s *service) Registry() *prometheus.Registry {
	return s.registry
}

// Collector returns the Prometheus collector.
func (s *service) Collector() *Collector {
	return s.collector
}

// Record saves a tool metric to the database (legacy method).
func (s *service) Record(ctx context.Context, metric ToolMetric) error {
	if s.q == nil {
		return nil
	}

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

// ObserveLLMRequest records an LLM request duration.
func (s *service) ObserveLLMRequest(provider, model string, duration time.Duration) {
	model = NormalizeModelName(model)
	s.collector.LLMRequestDuration.WithLabelValues(provider, model).Observe(duration.Seconds())
}

// ObserveTimeToFirstToken records time to first token.
func (s *service) ObserveTimeToFirstToken(provider, model string, duration time.Duration) {
	model = NormalizeModelName(model)
	s.collector.LLMTimeToFirstToken.WithLabelValues(provider, model).Observe(duration.Seconds())
}

// AddTokens increments token counters.
func (s *service) AddTokens(provider, model, tokenType string, count int64) {
	if count <= 0 {
		return
	}
	model = NormalizeModelName(model)
	s.collector.LLMTokensTotal.WithLabelValues(provider, model, tokenType).Add(float64(count))
}

// IncLLMRequest increments the LLM request counter.
func (s *service) IncLLMRequest(provider, model, status string) {
	model = NormalizeModelName(model)
	s.collector.LLMRequestsTotal.WithLabelValues(provider, model, status).Inc()
}

// ObserveTool records a tool execution duration and sizes.
func (s *service) ObserveTool(tool string, duration time.Duration, inputSize, outputSize int) {
	s.collector.ToolDuration.WithLabelValues(tool).Observe(duration.Seconds())
	if inputSize > 0 {
		s.collector.ToolInputBytes.WithLabelValues(tool).Observe(float64(inputSize))
	}
	if outputSize > 0 {
		s.collector.ToolOutputBytes.WithLabelValues(tool).Observe(float64(outputSize))
	}
}

// IncToolCall increments the tool call counter.
func (s *service) IncToolCall(tool, status string) {
	s.collector.ToolCallsTotal.WithLabelValues(tool, status).Inc()
}

// IncAgentStep increments the agent steps counter.
func (s *service) IncAgentStep() {
	s.collector.AgentStepsTotal.Inc()
}

// IncAgentRun increments the agent runs counter.
func (s *service) IncAgentRun(status string) {
	s.collector.AgentRunsTotal.WithLabelValues(status).Inc()
}

// IncAgentRetry increments the retry counter.
func (s *service) IncAgentRetry(provider, reason string) {
	reason = NormalizeErrorType(reason)
	s.collector.AgentRetriesTotal.WithLabelValues(provider, reason).Inc()
}

// IncSummarization increments the summarization counter.
func (s *service) IncSummarization() {
	s.collector.AgentSummarizationsTotal.Inc()
}

// IncLoopDetection increments the loop detection counter.
func (s *service) IncLoopDetection() {
	s.collector.AgentLoopDetectionsTotal.Inc()
}

// SetQueueDepth sets the current queue depth for a session.
func (s *service) SetQueueDepth(sessionID string, depth int) {
	s.collector.AgentQueueDepth.WithLabelValues(sessionID).Set(float64(depth))
}

// IncProviderError increments the provider error counter.
func (s *service) IncProviderError(provider string, statusCode int, errorType string) {
	errorType = NormalizeErrorType(errorType)
	s.collector.ProviderErrorsTotal.WithLabelValues(
		provider,
		strconv.Itoa(statusCode),
		errorType,
	).Inc()
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
	toolName := w.inner.Info().Name
	inputSize := len(params.Input)
	outputSize := len(resp.Content)
	success := err == nil && !resp.IsError

	// Record Prometheus metrics.
	status := "success"
	if !success {
		status = "error"
	}
	w.service.ObserveTool(toolName, duration, inputSize, outputSize)
	w.service.IncToolCall(toolName, status)

	// Also record to SQLite (legacy, async).
	sessionID := tools.GetSessionFromContext(ctx)
	if sessionID != "" && w.service.q != nil {
		metric := ToolMetric{
			SessionID:  sessionID,
			ToolName:   toolName,
			StartedAt:  startedAt,
			Duration:   duration,
			Success:    success,
			InputSize:  inputSize,
			OutputSize: outputSize,
		}
		if err != nil {
			metric.ErrorMessage = err.Error()
		} else if resp.IsError {
			metric.ErrorMessage = resp.Content
		}
		go func() {
			_ = w.service.Record(context.Background(), metric)
		}()
	}

	return resp, err
}

func (w *wrappedTool) ProviderOptions() fantasy.ProviderOptions {
	return w.inner.ProviderOptions()
}

func (w *wrappedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	w.inner.SetProviderOptions(opts)
}
