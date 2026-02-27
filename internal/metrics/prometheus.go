package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "crush"

// Histogram bucket definitions.
var (
	// LLM request duration: 100ms to 5min.
	llmDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

	// Time to first token: 50ms to 30s.
	ttftBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

	// Tool duration: 10ms to 2min.
	toolDurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 5, 10, 30, 60, 120}

	// Bytes: exponential from 100B to ~10MB (100 * 2^17 ≈ 13MB).
	bytesBuckets = prometheus.ExponentialBuckets(100, 2, 18)
)

// Collector holds all Prometheus metrics for Crush.
type Collector struct {
	// LLM metrics.
	LLMRequestDuration  *prometheus.HistogramVec
	LLMTimeToFirstToken *prometheus.HistogramVec
	LLMTokensTotal      *prometheus.CounterVec
	LLMRequestsTotal    *prometheus.CounterVec

	// Tool metrics.
	ToolDuration    *prometheus.HistogramVec
	ToolCallsTotal  *prometheus.CounterVec
	ToolInputBytes  *prometheus.HistogramVec
	ToolOutputBytes *prometheus.HistogramVec

	// Agent loop metrics.
	AgentStepsTotal          prometheus.Counter
	AgentRunsTotal           *prometheus.CounterVec
	AgentRetriesTotal        *prometheus.CounterVec
	AgentSummarizationsTotal prometheus.Counter
	AgentLoopDetectionsTotal prometheus.Counter
	AgentQueueDepth          *prometheus.GaugeVec

	// Provider error metrics.
	ProviderErrorsTotal *prometheus.CounterVec
}

// NewCollector creates and registers all Prometheus metrics.
func NewCollector(registry prometheus.Registerer) *Collector {
	factory := promauto.With(registry)

	return &Collector{
		// LLM metrics.
		LLMRequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "llm",
				Name:      "request_duration_seconds",
				Help:      "Duration of LLM requests in seconds.",
				Buckets:   llmDurationBuckets,
			},
			[]string{"provider", "model"},
		),
		LLMTimeToFirstToken: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "llm",
				Name:      "time_to_first_token_seconds",
				Help:      "Time until first token is received in seconds.",
				Buckets:   ttftBuckets,
			},
			[]string{"provider", "model"},
		),
		LLMTokensTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "llm",
				Name:      "tokens_total",
				Help:      "Total number of tokens processed.",
			},
			[]string{"provider", "model", "type"},
		),
		LLMRequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "llm",
				Name:      "requests_total",
				Help:      "Total number of LLM requests.",
			},
			[]string{"provider", "model", "status"},
		),

		// Tool metrics.
		ToolDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "tool",
				Name:      "duration_seconds",
				Help:      "Duration of tool executions in seconds.",
				Buckets:   toolDurationBuckets,
			},
			[]string{"tool"},
		),
		ToolCallsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "tool",
				Name:      "calls_total",
				Help:      "Total number of tool calls.",
			},
			[]string{"tool", "status"},
		),
		ToolInputBytes: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "tool",
				Name:      "input_bytes",
				Help:      "Size of tool input in bytes.",
				Buckets:   bytesBuckets,
			},
			[]string{"tool"},
		),
		ToolOutputBytes: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "tool",
				Name:      "output_bytes",
				Help:      "Size of tool output in bytes.",
			},
			[]string{"tool"},
		),

		// Agent loop metrics.
		AgentStepsTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "agent",
				Name:      "steps_total",
				Help:      "Total number of agent steps executed.",
			},
		),
		AgentRunsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "agent",
				Name:      "runs_total",
				Help:      "Total number of agent runs.",
			},
			[]string{"status"},
		),
		AgentRetriesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "agent",
				Name:      "retries_total",
				Help:      "Total number of provider retries.",
			},
			[]string{"provider", "reason"},
		),
		AgentSummarizationsTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "agent",
				Name:      "summarizations_total",
				Help:      "Total number of auto-summarization triggers.",
			},
		),
		AgentLoopDetectionsTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "agent",
				Name:      "loop_detections_total",
				Help:      "Total number of loop detection triggers.",
			},
		),
		AgentQueueDepth: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "agent",
				Name:      "queue_depth",
				Help:      "Current message queue depth per session.",
			},
			[]string{"session_id"},
		),

		// Provider error metrics.
		ProviderErrorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "provider",
				Name:      "errors_total",
				Help:      "Total number of provider errors.",
			},
			[]string{"provider", "status_code", "error_type"},
		),
	}
}

// NormalizeErrorType maps error types to a low-cardinality enum.
func NormalizeErrorType(errType string) string {
	switch errType {
	case "rate_limit", "rate_limit_error":
		return "rate_limit"
	case "authentication_error", "invalid_api_key", "unauthorized":
		return "auth"
	case "server_error", "internal_error", "internal_server_error":
		return "server_error"
	case "timeout", "request_timeout", "deadline_exceeded":
		return "timeout"
	case "invalid_request", "bad_request", "validation_error":
		return "invalid_request"
	case "overloaded", "overloaded_error":
		return "overloaded"
	default:
		return "other"
	}
}

// NormalizeModelName strips version hashes and normalizes model names.
func NormalizeModelName(model string) string {
	// For now, return as-is. Can add normalization logic later if needed.
	// e.g., strip "@version" suffixes, normalize casing, etc.
	return model
}
