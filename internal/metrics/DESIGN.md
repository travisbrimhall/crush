# Prometheus Metrics Tech Design

## Overview

Add comprehensive Prometheus instrumentation to Crush for real-time observability
via Grafana dashboards. This replaces the SQLite-based tool metrics with
in-memory Prometheus metrics that can be scraped by a local Prometheus instance.

## Goals

1. Track LLM request performance (latency, TTFT, tokens)
2. Track tool execution performance and errors
3. Track agent loop behavior (steps, retries, summarization)
4. Track provider errors by type and provider
5. Expose metrics via HTTP endpoint for Prometheus scraping

## Non-Goals

- Remote metrics shipping (Grafana Cloud, etc.) - future work
- Replacing structured logging with metrics
- Historical metrics storage (Prometheus handles retention)

## Metrics Catalog

### LLM Request Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `crush_llm_request_duration_seconds` | Histogram | `provider`, `model`, `status` | Total request duration |
| `crush_llm_time_to_first_token_seconds` | Histogram | `provider`, `model` | Time until first token streamed |
| `crush_llm_tokens_total` | Counter | `provider`, `model`, `type` | Token counts (type: input/output/cache_read/cache_write) |
| `crush_llm_requests_total` | Counter | `provider`, `model`, `status` | Request counts (status: success/error/canceled) |

### Tool Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `crush_tool_duration_seconds` | Histogram | `tool`, `status` | Tool execution duration |
| `crush_tool_calls_total` | Counter | `tool`, `status` | Tool call counts (status: success/error) |
| `crush_tool_input_bytes` | Histogram | `tool` | Tool input size distribution |
| `crush_tool_output_bytes` | Histogram | `tool` | Tool output size distribution |

### Agent Loop Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `crush_agent_steps_total` | Counter | `session_id` | Steps per agent run |
| `crush_agent_runs_total` | Counter | `status` | Agent run counts (status: success/error/canceled/summarized) |
| `crush_agent_retries_total` | Counter | `provider`, `reason` | Provider retry counts |
| `crush_agent_summarizations_total` | Counter | | Auto-summarization triggers |
| `crush_agent_loop_detections_total` | Counter | | Loop detection triggers |
| `crush_agent_queue_depth` | Gauge | `session_id` | Current message queue depth |

### Provider Error Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `crush_provider_errors_total` | Counter | `provider`, `status_code`, `error_type` | Provider errors by type |

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Crush Process                           │
│                                                                 │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐      │
│  │ StreamRunner │───▶│ StepHandler  │───▶│   metrics    │      │
│  └──────────────┘    └──────────────┘    │   package    │      │
│         │                   │            │              │      │
│         │            ┌──────┴──────┐     │ ┌──────────┐ │      │
│         │            │ Tool Wrapper│────▶│ │Collectors│ │      │
│         │            └─────────────┘     │ └──────────┘ │      │
│         │                                │      │       │      │
│         ▼                                │      ▼       │      │
│  ┌──────────────┐                        │ ┌──────────┐ │      │
│  │ ErrorHandler │───────────────────────▶│ │ Registry │ │      │
│  └──────────────┘                        │ └──────────┘ │      │
│                                          └──────┬───────┘      │
│                                                 │              │
│                                          ┌──────▼───────┐      │
│                                          │ HTTP Handler │      │
│                                          │  /metrics    │      │
│                                          └──────────────┘      │
└─────────────────────────────────────────────────────────────────┘
                                                  │
                                                  ▼
                                          ┌──────────────┐
                                          │  Prometheus  │
                                          │   (scrape)   │
                                          └──────────────┘
                                                  │
                                                  ▼
                                          ┌──────────────┐
                                          │   Grafana    │
                                          │ (dashboards) │
                                          └──────────────┘
```

## Package Design

### `internal/metrics/prometheus.go`

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// Collector holds all Prometheus metrics.
type Collector struct {
    // LLM metrics
    LLMRequestDuration    *prometheus.HistogramVec
    LLMTimeToFirstToken   *prometheus.HistogramVec
    LLMTokensTotal        *prometheus.CounterVec
    LLMRequestsTotal      *prometheus.CounterVec

    // Tool metrics
    ToolDuration          *prometheus.HistogramVec
    ToolCallsTotal        *prometheus.CounterVec
    ToolInputBytes        *prometheus.HistogramVec
    ToolOutputBytes       *prometheus.HistogramVec

    // Agent metrics
    AgentStepsTotal       *prometheus.CounterVec
    AgentRunsTotal        *prometheus.CounterVec
    AgentRetriesTotal     *prometheus.CounterVec
    AgentSummarizationsTotal prometheus.Counter
    AgentLoopDetectionsTotal prometheus.Counter
    AgentQueueDepth       *prometheus.GaugeVec

    // Provider errors
    ProviderErrorsTotal   *prometheus.CounterVec
}

func NewCollector() *Collector { ... }
```

### Updated `internal/metrics/metrics.go`

The existing `Service` interface will be extended:

```go
type Service interface {
    // Existing (keep for backward compat, but deprecated)
    Record(ctx context.Context, metric ToolMetric) error
    WrapTool(tool fantasy.AgentTool) fantasy.AgentTool

    // New Prometheus methods
    Collector() *Collector
    
    // LLM tracking
    ObserveLLMRequest(provider, model, status string, duration time.Duration)
    ObserveTimeToFirstToken(provider, model string, duration time.Duration)
    AddTokens(provider, model, tokenType string, count int64)
    
    // Tool tracking (replaces Record)
    ObserveTool(tool, status string, duration time.Duration, inputSize, outputSize int)
    
    // Agent tracking
    IncAgentSteps(sessionID string)
    IncAgentRun(status string)
    IncAgentRetry(provider, reason string)
    IncSummarization()
    IncLoopDetection()
    SetQueueDepth(sessionID string, depth int)
    
    // Provider errors
    IncProviderError(provider string, statusCode int, errorType string)
}
```

## Integration Points

### 1. StepHandler (LLM metrics)

```go
// In PrepareStep - record start time
h.stepStartTime = time.Now()

// In OnTextDelta (first call) - record TTFT
if h.firstTokenTime.IsZero() {
    h.firstTokenTime = time.Now()
    h.metrics.ObserveTimeToFirstToken(provider, model, time.Since(h.stepStartTime))
}

// In OnStepFinish - record total duration and tokens
h.metrics.ObserveLLMRequest(provider, model, "success", time.Since(h.stepStartTime))
h.metrics.AddTokens(provider, model, "input", usage.InputTokens)
h.metrics.AddTokens(provider, model, "output", usage.OutputTokens)
h.metrics.AddTokens(provider, model, "cache_read", usage.CacheReadTokens)
h.metrics.AddTokens(provider, model, "cache_write", usage.CacheCreationTokens)
```

### 2. Tool Wrapper (tool metrics)

The existing `WrapTool` already captures timing. Update to use Prometheus:

```go
func (w *wrappedTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
    start := time.Now()
    resp, err := w.inner.Run(ctx, params)
    duration := time.Since(start)

    status := "success"
    if err != nil || resp.IsError {
        status = "error"
    }

    w.collector.ToolDuration.WithLabelValues(w.inner.Info().Name, status).Observe(duration.Seconds())
    w.collector.ToolCallsTotal.WithLabelValues(w.inner.Info().Name, status).Inc()
    w.collector.ToolInputBytes.WithLabelValues(w.inner.Info().Name).Observe(float64(len(params.Input)))
    w.collector.ToolOutputBytes.WithLabelValues(w.inner.Info().Name).Observe(float64(len(resp.Content)))

    return resp, err
}
```

### 3. StreamRunner (agent loop metrics)

```go
// In Run()
h.metrics.IncAgentSteps(sessionID)

// After loop completes
status := "success"
if err != nil {
    if errors.Is(err, context.Canceled) {
        status = "canceled"
    } else {
        status = "error"
    }
}
if state.ShouldSummarize {
    status = "summarized"
    h.metrics.IncSummarization()
}
h.metrics.IncAgentRun(status)
```

### 4. Error Handler (provider errors)

```go
// In handleStreamError or coordinator retry logic
var providerErr *fantasy.ProviderError
if errors.As(err, &providerErr) {
    h.metrics.IncProviderError(provider, providerErr.StatusCode, providerErr.Type)
}
```

### 5. OnRetry callback

```go
func (h *StepHandler) OnRetry(err *fantasy.ProviderError, delay time.Duration) {
    reason := "unknown"
    if err != nil {
        reason = err.Type // "rate_limit", "server_error", etc.
    }
    h.metrics.IncAgentRetry(h.state.Model.ModelCfg.Provider, reason)
}
```

## HTTP Endpoint

Add a `/metrics` endpoint. Two options:

### Option A: Separate metrics server (recommended)

Start a small HTTP server on a configurable port (default: 9090):

```go
// In app.go or cmd/run.go
if cfg.MetricsEnabled {
    go func() {
        http.Handle("/metrics", promhttp.Handler())
        http.ListenAndServe(":9090", nil)
    }()
}
```

Pros: Clean separation, standard Prometheus pattern
Cons: Extra port to manage

### Option B: Integrate with existing SSH server

If running in server mode, add `/metrics` to the same mux.

## Configuration

Add to config:

```yaml
metrics:
  enabled: true
  port: 9090
```

Or environment variable: `CRUSH_METRICS_PORT=9090`

## Histogram Buckets

Use sensible defaults for latency distributions:

```go
// LLM request duration: 100ms to 5min
LLMDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// TTFT: 50ms to 30s  
TTFTBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// Tool duration: 10ms to 2min
ToolDurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 5, 10, 30, 60, 120}

// Bytes: 100B to 10MB
BytesBuckets = []float64{100, 1000, 10000, 100000, 1000000, 10000000}
```

## Migration Path

1. Keep existing SQLite `tool_metrics` table for now (historical data)
2. Add Prometheus metrics alongside
3. Deprecate SQLite recording after Prometheus is stable
4. Eventually remove SQLite metrics in future release

## Files to Create/Modify

### New Files
- `internal/metrics/prometheus.go` - Collector and metric definitions
- `internal/metrics/http.go` - HTTP handler for /metrics endpoint

### Modified Files
- `internal/metrics/metrics.go` - Extend Service interface
- `internal/agent/step_handler.go` - Add LLM metric calls
- `internal/agent/stream_runner.go` - Add agent loop metrics
- `internal/agent/error_handler.go` - Add provider error metrics
- `internal/agent/agent.go` - Pass metrics to StepHandler
- `internal/agent/coordinator.go` - Initialize metrics, pass through
- `internal/app/app.go` - Start metrics HTTP server
- `internal/config/config.go` - Add metrics config

## Testing

1. Unit tests for Collector initialization
2. Integration test that runs agent and verifies metrics are recorded
3. Manual test with local Prometheus + Grafana

## Grafana Dashboard (future)

Provide a JSON dashboard definition covering:
- LLM latency percentiles over time
- Token usage rate
- Tool execution breakdown
- Error rates by provider
- Agent loop behavior

## Open Questions

1. **Cardinality**: Should `session_id` be a label? Could explode cardinality.
   - Recommendation: Only use for gauges (queue depth), not counters/histograms
   
2. **Metric prefix**: `crush_` vs `llm_` vs something else?
   - Recommendation: `crush_` for namespacing

3. **Default enabled?**: Should metrics server start by default?
   - Recommendation: Off by default, opt-in via config/env var
