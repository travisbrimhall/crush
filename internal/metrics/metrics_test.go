package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestNewCollector(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	collector := NewCollector(registry)

	require.NotNil(t, collector)
	require.NotNil(t, collector.LLMRequestDuration)
	require.NotNil(t, collector.LLMTimeToFirstToken)
	require.NotNil(t, collector.LLMTokensTotal)
	require.NotNil(t, collector.ToolDuration)
	require.NotNil(t, collector.AgentStepsTotal)
}

func TestServiceLLMMetrics(t *testing.T) {
	t.Parallel()
	svc := NewWithOptions(Options{})

	// Record LLM request.
	svc.ObserveLLMRequest("anthropic", "claude-3", 2*time.Second)
	svc.ObserveTimeToFirstToken("anthropic", "claude-3", 500*time.Millisecond)
	svc.AddTokens("anthropic", "claude-3", "input", 100)
	svc.AddTokens("anthropic", "claude-3", "output", 50)
	svc.IncLLMRequest("anthropic", "claude-3", "success")

	// Verify metrics were recorded.
	metrics, err := svc.Registry().Gather()
	require.NoError(t, err)

	found := findMetric(metrics, "crush_llm_requests_total")
	require.NotNil(t, found, "crush_llm_requests_total should exist")

	found = findMetric(metrics, "crush_llm_tokens_total")
	require.NotNil(t, found, "crush_llm_tokens_total should exist")
}

func TestServiceToolMetrics(t *testing.T) {
	t.Parallel()
	svc := NewWithOptions(Options{})

	// Record tool execution.
	svc.ObserveTool("bash", 100*time.Millisecond, 50, 200)
	svc.IncToolCall("bash", "success")
	svc.IncToolCall("bash", "error")

	// Verify metrics.
	metrics, err := svc.Registry().Gather()
	require.NoError(t, err)

	found := findMetric(metrics, "crush_tool_calls_total")
	require.NotNil(t, found, "crush_tool_calls_total should exist")
}

func TestServiceAgentMetrics(t *testing.T) {
	t.Parallel()
	svc := NewWithOptions(Options{})

	// Record agent metrics.
	svc.IncAgentStep()
	svc.IncAgentStep()
	svc.IncAgentRun("success")
	svc.IncAgentRetry("anthropic", "rate_limit")
	svc.IncSummarization()
	svc.IncLoopDetection()
	svc.SetQueueDepth("session-123", 3)

	// Verify metrics.
	metrics, err := svc.Registry().Gather()
	require.NoError(t, err)

	require.NotNil(t, findMetric(metrics, "crush_agent_steps_total"))
	require.NotNil(t, findMetric(metrics, "crush_agent_runs_total"))
	require.NotNil(t, findMetric(metrics, "crush_agent_retries_total"))
	require.NotNil(t, findMetric(metrics, "crush_agent_summarizations_total"))
	require.NotNil(t, findMetric(metrics, "crush_agent_loop_detections_total"))
	require.NotNil(t, findMetric(metrics, "crush_agent_queue_depth"))
}

func TestServiceProviderErrorMetrics(t *testing.T) {
	t.Parallel()
	svc := NewWithOptions(Options{})

	svc.IncProviderError("openai", 429, "rate_limit")
	svc.IncProviderError("anthropic", 500, "server_error")

	metrics, err := svc.Registry().Gather()
	require.NoError(t, err)

	require.NotNil(t, findMetric(metrics, "crush_provider_errors_total"))
}

func TestNormalizeErrorType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"rate_limit", "rate_limit"},
		{"rate_limit_error", "rate_limit"},
		{"authentication_error", "auth"},
		{"invalid_api_key", "auth"},
		{"server_error", "server_error"},
		{"internal_server_error", "server_error"},
		{"timeout", "timeout"},
		{"request_timeout", "timeout"},
		{"unknown_error", "other"},
		{"", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizeErrorType(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestRecordWithNilDB(t *testing.T) {
	t.Parallel()
	svc := NewWithOptions(Options{DB: nil})

	// Should not panic with nil DB.
	err := svc.Record(context.Background(), ToolMetric{
		SessionID: "test",
		ToolName:  "bash",
		StartedAt: time.Now(),
		Duration:  time.Second,
		Success:   true,
	})
	require.NoError(t, err)
}

func findMetric(metrics []*io_prometheus_client.MetricFamily, name string) *io_prometheus_client.MetricFamily {
	for _, m := range metrics {
		if m.GetName() == name {
			return m
		}
	}
	return nil
}
