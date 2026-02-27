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
	return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
}
