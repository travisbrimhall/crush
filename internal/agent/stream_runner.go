/*
Package agent implements the orchestration of streaming AI interactions using
the fantasy agent framework.

# Architectural Overview

The system follows a layered streaming orchestration pattern:

 1. StreamRunner – The orchestration layer.
    Responsible for initiating a single streaming session. Wires together the
    agent, step handler callbacks, and stop conditions. Separates cancellable
    user contexts (streamCtx) from persistent contexts (parentCtx) for DB
    operations.

 2. StepHandler – The callback layer.
    Handles fine-grained streaming events from the AI agent (reasoning deltas,
    text deltas, tool calls, retries, step finishes). Maintains per-step state,
    updates persistent messages, and coordinates with auxiliary systems (LSP,
    session service). Encapsulates all provider-specific logic, signatures, and
    transformations.

 3. RunState – The state layer.
    Centralizes session state, assistant message state, LSP batchers, and
    queued message management. Used by both StreamRunner and StepHandler to
    maintain a single source of truth per streaming interaction.

# Key Patterns

  - Callback-Driven Streaming: All AI output is processed incrementally via
    StepHandler callbacks (OnTextDelta, OnReasoningDelta, etc.), decoupling
    processing logic from the agent itself.

  - Immutable Message Replacement: Updates to messages (tool calls, reasoning
    content) are applied in-place by ID, ensuring no duplication and preserving
    message order.

  - Context Separation: parentCtx survives cancellation for DB/session
    operations; streamCtx is cancellable for user-initiated stop or timeout.

  - Stop Conditions as Strategy: Stop conditions (autoSummarizeCondition,
    loopDetectionCondition) are first-class functions injected into the
    streaming loop, allowing easy extension or modification.

  - Provider Abstraction Layer: Provider-specific logic (Anthropic, OpenAI,
    Google) is isolated in reasoning extraction and metadata handling, keeping
    core orchestration agnostic.

  - Queued Message Replay: Messages sent while a streaming step is preparing
    are stored in a queue and replayed into the AI message sequence, preserving
    order and consistency.

  - LSP Batching: When code/file operations are involved, updates are batched
    to reduce overhead, decoupling AI reasoning from tooling notifications.

# Best Practices

 1. Keep StepHandler stateless except for references to RunState. Avoid storing
    persistent agent or session state in local variables.

 2. Always update messages via the messages service; never mutate
    RunState.CurrentAssistant directly outside these callbacks.

 3. Use AddToolCall with ID replacement semantics to prevent duplicate tool
    call entries.

 4. Maintain the separation of contexts (parentCtx vs streamCtx) for
    cancellation safety.

 5. Keep provider-specific transformations and metadata extraction in
    StepHandler, not in StreamRunner.

This layered approach ensures high observability of AI streaming behavior, easy
testing of individual steps, robust handling of retries/cancellations/tool
calls, and flexibility to add new AI providers or stop conditions without
changing core orchestration.
*/
package agent

import (
	"context"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
)

// StreamRunner executes a single streaming interaction with the fantasy agent.
// It wires up the StepHandler callbacks and stop conditions.
type StreamRunner struct {
	agent   fantasy.Agent
	handler *StepHandler
	state   *RunState
	call    SessionAgentCall

	// disableAutoSummarize disables the auto-summarization stop condition.
	disableAutoSummarize bool
}

// NewStreamRunner creates a runner for the given agent and handler.
func NewStreamRunner(
	agent fantasy.Agent,
	handler *StepHandler,
	state *RunState,
	call SessionAgentCall,
	disableAutoSummarize bool,
) *StreamRunner {
	return &StreamRunner{
		agent:                agent,
		handler:              handler,
		state:                state,
		call:                 call,
		disableAutoSummarize: disableAutoSummarize,
	}
}

// Run executes the streaming interaction and returns the result.
// The parentCtx is used for DB operations that should survive cancellation.
// The streamCtx should be cancellable for user-initiated stops.
func (r *StreamRunner) Run(parentCtx, streamCtx context.Context) (*fantasy.AgentResult, error) {
	r.handler.SetStreamContext(streamCtx)

	return r.agent.Stream(streamCtx, fantasy.AgentStreamCall{
		Prompt:           message.PromptWithTextAttachments(r.call.Prompt, r.call.Attachments),
		Files:            r.state.Files,
		Messages:         r.state.History,
		ProviderOptions:  r.call.ProviderOptions,
		MaxOutputTokens:  &r.call.MaxOutputTokens,
		TopP:             r.call.TopP,
		Temperature:      r.call.Temperature,
		PresencePenalty:  r.call.PresencePenalty,
		TopK:             r.call.TopK,
		FrequencyPenalty: r.call.FrequencyPenalty,
		PrepareStep:      r.handler.PrepareStep,
		OnReasoningStart: r.handler.OnReasoningStart,
		OnReasoningDelta: r.handler.OnReasoningDelta,
		OnReasoningEnd:   r.handler.OnReasoningEnd,
		OnTextDelta:      r.handler.OnTextDelta,
		OnToolInputStart: r.handler.OnToolInputStart,
		OnToolCall:       r.handler.OnToolCall,
		OnToolResult:     r.handler.OnToolResult,
		OnRetry:          r.handler.OnRetry,
		OnStepFinish: func(stepResult fantasy.StepResult) error {
			return r.handler.OnStepFinish(parentCtx, stepResult)
		},
		StopWhen: r.buildStopConditions(),
	})
}

// buildStopConditions creates the stop conditions for the streaming loop.
func (r *StreamRunner) buildStopConditions() []fantasy.StopCondition {
	return []fantasy.StopCondition{
		r.autoSummarizeCondition,
		r.loopDetectionCondition,
	}
}

// autoSummarizeCondition checks if the context window is nearly full.
func (r *StreamRunner) autoSummarizeCondition(_ []fantasy.StepResult) bool {
	if r.disableAutoSummarize {
		return false
	}

	cw := int64(r.state.Model.CatwalkCfg.ContextWindow)
	sess := r.state.GetSession()
	tokens := sess.CompletionTokens + sess.PromptTokens
	remaining := cw - tokens

	var threshold int64
	if cw > largeContextWindowThreshold {
		threshold = largeContextWindowBuffer
	} else {
		threshold = int64(float64(cw) * smallContextWindowRatio)
	}

	if remaining <= threshold {
		r.state.ShouldSummarize = true
		return true
	}
	return false
}

// loopDetectionCondition checks for repeated tool call patterns.
func (r *StreamRunner) loopDetectionCondition(steps []fantasy.StepResult) bool {
	detected := hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
	if detected && r.handler.metrics != nil {
		r.handler.metrics.IncLoopDetection()
	}
	return detected
}
