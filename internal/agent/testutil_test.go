package agent

import (
	"context"
	"errors"
	"sync"

	"charm.land/fantasy"
)

// StreamEvent represents a single event in a scripted streaming sequence.
// Events are emitted in order by ScriptedAgent.Stream().
type StreamEvent interface {
	isStreamEvent()
}

// TextDeltaEvent emits a text delta to OnTextDelta callback.
type TextDeltaEvent struct {
	ID   string
	Text string
}

func (TextDeltaEvent) isStreamEvent() {}

// ReasoningStartEvent emits reasoning start to OnReasoningStart callback.
type ReasoningStartEvent struct {
	ID   string
	Text string
}

func (ReasoningStartEvent) isStreamEvent() {}

// ReasoningDeltaEvent emits reasoning delta to OnReasoningDelta callback.
type ReasoningDeltaEvent struct {
	ID   string
	Text string
}

func (ReasoningDeltaEvent) isStreamEvent() {}

// ReasoningEndEvent emits reasoning end to OnReasoningEnd callback.
type ReasoningEndEvent struct {
	ID       string
	Metadata fantasy.ProviderMetadata
}

func (ReasoningEndEvent) isStreamEvent() {}

// ToolInputStartEvent emits tool input start to OnToolInputStart callback.
type ToolInputStartEvent struct {
	ID       string
	ToolName string
}

func (ToolInputStartEvent) isStreamEvent() {}

// ToolCallEvent emits a complete tool call to OnToolCall callback.
type ToolCallEvent struct {
	ID       string
	ToolName string
	Input    string
}

func (ToolCallEvent) isStreamEvent() {}

// ToolResultEvent emits a tool result to OnToolResult callback.
// This simulates the tool execution completing.
type ToolResultEvent struct {
	ID      string
	Name    string
	Output  string
	IsError bool
}

func (ToolResultEvent) isStreamEvent() {}

// ErrorEvent causes the stream to return an error.
type ErrorEvent struct {
	Err error
}

func (ErrorEvent) isStreamEvent() {}

// CancelEvent triggers context cancellation mid-stream.
// The test must provide a cancellable context.
type CancelEvent struct{}

func (CancelEvent) isStreamEvent() {}

// FinishEvent ends the current step with the given finish reason.
type FinishEvent struct {
	Reason fantasy.FinishReason
	Usage  fantasy.Usage
}

func (FinishEvent) isStreamEvent() {}

// StepBoundaryEvent marks the end of one step and start of another.
// Used to simulate multi-step agent interactions.
type StepBoundaryEvent struct{}

func (StepBoundaryEvent) isStreamEvent() {}

// ScriptedAgent implements fantasy.Agent with a predetermined script.
// It emits events in order through the AgentStreamCall callbacks.
type ScriptedAgent struct {
	script []StreamEvent
	tools  []fantasy.AgentTool

	mu       sync.Mutex
	cancelFn context.CancelFunc
}

// NewScriptedAgent creates an agent that follows the given script.
func NewScriptedAgent(script []StreamEvent, tools ...fantasy.AgentTool) *ScriptedAgent {
	return &ScriptedAgent{
		script: script,
		tools:  tools,
	}
}

// SetCancelFunc allows tests to inject the cancel function for CancelEvent.
func (a *ScriptedAgent) SetCancelFunc(fn context.CancelFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelFn = fn
}

// Generate implements fantasy.Agent but is not used in these tests.
func (a *ScriptedAgent) Generate(_ context.Context, _ fantasy.AgentCall) (*fantasy.AgentResult, error) {
	return nil, errors.New("ScriptedAgent.Generate not implemented")
}

// Stream implements fantasy.Agent by emitting scripted events through callbacks.
func (a *ScriptedAgent) Stream(ctx context.Context, call fantasy.AgentStreamCall) (*fantasy.AgentResult, error) {
	var steps []fantasy.StepResult
	var currentStepMessages []fantasy.Message
	var totalUsage fantasy.Usage

	// Call PrepareStep if provided.
	if call.PrepareStep != nil {
		var err error
		var prepared fantasy.PrepareStepResult
		ctx, prepared, err = call.PrepareStep(ctx, fantasy.PrepareStepFunctionOptions{
			Messages: call.Messages,
		})
		if err != nil {
			return nil, err
		}
		currentStepMessages = prepared.Messages
	}

	// Track tool calls for creating tool result messages.
	pendingToolCalls := make(map[string]fantasy.ToolCallContent)

	for _, event := range a.script {
		// Check for cancellation before each event.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if call.OnTextDelta != nil {
				if err := call.OnTextDelta(e.ID, e.Text); err != nil {
					return nil, err
				}
			}

		case ReasoningStartEvent:
			if call.OnReasoningStart != nil {
				if err := call.OnReasoningStart(e.ID, fantasy.ReasoningContent{Text: e.Text}); err != nil {
					return nil, err
				}
			}

		case ReasoningDeltaEvent:
			if call.OnReasoningDelta != nil {
				if err := call.OnReasoningDelta(e.ID, e.Text); err != nil {
					return nil, err
				}
			}

		case ReasoningEndEvent:
			if call.OnReasoningEnd != nil {
				if err := call.OnReasoningEnd(e.ID, fantasy.ReasoningContent{
					ProviderMetadata: e.Metadata,
				}); err != nil {
					return nil, err
				}
			}

		case ToolInputStartEvent:
			if call.OnToolInputStart != nil {
				if err := call.OnToolInputStart(e.ID, e.ToolName); err != nil {
					return nil, err
				}
			}

		case ToolCallEvent:
			tc := fantasy.ToolCallContent{
				ToolCallID: e.ID,
				ToolName:   e.ToolName,
				Input:      e.Input,
			}
			pendingToolCalls[e.ID] = tc

			if call.OnToolCall != nil {
				if err := call.OnToolCall(tc); err != nil {
					return nil, err
				}
			}

			// Execute the tool if registered.
			for _, tool := range a.tools {
				if tool.Info().Name == e.ToolName {
					toolCall := fantasy.ToolCall{
						ID:    e.ID,
						Name:  e.ToolName,
						Input: e.Input,
					}
					resp, execErr := tool.Run(ctx, toolCall)
					var output fantasy.ToolResultOutputContent
					if execErr != nil {
						output = fantasy.ToolResultOutputContentError{Error: execErr}
					} else if resp.IsError {
						output = fantasy.ToolResultOutputContentError{Error: errors.New(resp.Content)}
					} else {
						output = fantasy.ToolResultOutputContentText{Text: resp.Content}
					}
					tr := fantasy.ToolResultContent{
						ToolCallID: e.ID,
						ToolName:   e.ToolName,
						Result:     output,
					}
					if call.OnToolResult != nil {
						if err := call.OnToolResult(tr); err != nil {
							return nil, err
						}
					}
					break
				}
			}

		case ToolResultEvent:
			// Manual tool result injection (for tests that don't use real tools).
			var output fantasy.ToolResultOutputContent
			if e.IsError {
				output = fantasy.ToolResultOutputContentError{Error: errors.New(e.Output)}
			} else {
				output = fantasy.ToolResultOutputContentText{Text: e.Output}
			}
			tr := fantasy.ToolResultContent{
				ToolCallID: e.ID,
				ToolName:   e.Name,
				Result:     output,
			}
			if call.OnToolResult != nil {
				if err := call.OnToolResult(tr); err != nil {
					return nil, err
				}
			}

		case ErrorEvent:
			return nil, e.Err

		case CancelEvent:
			a.mu.Lock()
			fn := a.cancelFn
			a.mu.Unlock()
			if fn != nil {
				fn()
			}
			// Immediately return the cancellation error.
			return nil, context.Canceled

		case FinishEvent:
			totalUsage.InputTokens += e.Usage.InputTokens
			totalUsage.OutputTokens += e.Usage.OutputTokens

			stepResult := fantasy.StepResult{
				Response: fantasy.Response{
					FinishReason: e.Reason,
					Usage:        e.Usage,
				},
				Messages: currentStepMessages,
			}
			steps = append(steps, stepResult)

			if call.OnStepFinish != nil {
				if err := call.OnStepFinish(stepResult); err != nil {
					return nil, err
				}
			}

		case StepBoundaryEvent:
			// Reset for next step - call PrepareStep again.
			if call.PrepareStep != nil {
				var err error
				var prepared fantasy.PrepareStepResult
				ctx, prepared, err = call.PrepareStep(ctx, fantasy.PrepareStepFunctionOptions{
					Messages: currentStepMessages,
				})
				if err != nil {
					return nil, err
				}
				currentStepMessages = prepared.Messages
			}
		}
	}

	// Build final result.
	var finalResponse fantasy.Response
	if len(steps) > 0 {
		finalResponse = steps[len(steps)-1].Response
	}

	return &fantasy.AgentResult{
		Steps:      steps,
		Response:   finalResponse,
		TotalUsage: totalUsage,
	}, nil
}

// ScriptBuilder provides a fluent API for building test scripts.
type ScriptBuilder struct {
	events []StreamEvent
}

// NewScript creates a new script builder.
func NewScript() *ScriptBuilder {
	return &ScriptBuilder{}
}

// TextDelta adds a text delta event.
func (b *ScriptBuilder) TextDelta(text string) *ScriptBuilder {
	b.events = append(b.events, TextDeltaEvent{ID: "text", Text: text})
	return b
}

// ReasoningStart adds a reasoning start event.
func (b *ScriptBuilder) ReasoningStart(text string) *ScriptBuilder {
	b.events = append(b.events, ReasoningStartEvent{ID: "reasoning", Text: text})
	return b
}

// ReasoningDelta adds a reasoning delta event.
func (b *ScriptBuilder) ReasoningDelta(text string) *ScriptBuilder {
	b.events = append(b.events, ReasoningDeltaEvent{ID: "reasoning", Text: text})
	return b
}

// ReasoningEnd adds a reasoning end event.
func (b *ScriptBuilder) ReasoningEnd() *ScriptBuilder {
	b.events = append(b.events, ReasoningEndEvent{ID: "reasoning"})
	return b
}

// ToolStart adds a tool input start event.
func (b *ScriptBuilder) ToolStart(id, name string) *ScriptBuilder {
	b.events = append(b.events, ToolInputStartEvent{ID: id, ToolName: name})
	return b
}

// ToolCall adds a complete tool call event.
func (b *ScriptBuilder) ToolCall(id, name, input string) *ScriptBuilder {
	b.events = append(b.events, ToolCallEvent{ID: id, ToolName: name, Input: input})
	return b
}

// ToolResult adds a tool result event.
func (b *ScriptBuilder) ToolResult(id, name, output string) *ScriptBuilder {
	b.events = append(b.events, ToolResultEvent{ID: id, Name: name, Output: output})
	return b
}

// ToolError adds a tool error result event.
func (b *ScriptBuilder) ToolError(id, name, errMsg string) *ScriptBuilder {
	b.events = append(b.events, ToolResultEvent{ID: id, Name: name, Output: errMsg, IsError: true})
	return b
}

// Error adds an error event.
func (b *ScriptBuilder) Error(err error) *ScriptBuilder {
	b.events = append(b.events, ErrorEvent{Err: err})
	return b
}

// Cancel adds a cancel event.
func (b *ScriptBuilder) Cancel() *ScriptBuilder {
	b.events = append(b.events, CancelEvent{})
	return b
}

// Finish adds a finish event with stop reason.
func (b *ScriptBuilder) Finish() *ScriptBuilder {
	b.events = append(b.events, FinishEvent{Reason: fantasy.FinishReasonStop})
	return b
}

// FinishWithReason adds a finish event with specified reason.
func (b *ScriptBuilder) FinishWithReason(reason fantasy.FinishReason) *ScriptBuilder {
	b.events = append(b.events, FinishEvent{Reason: reason})
	return b
}

// FinishToolUse adds a finish event with tool calls reason.
func (b *ScriptBuilder) FinishToolUse() *ScriptBuilder {
	b.events = append(b.events, FinishEvent{Reason: fantasy.FinishReasonToolCalls})
	return b
}

// StepBoundary adds a step boundary for multi-step interactions.
func (b *ScriptBuilder) StepBoundary() *ScriptBuilder {
	b.events = append(b.events, StepBoundaryEvent{})
	return b
}

// Build returns the constructed script.
func (b *ScriptBuilder) Build() []StreamEvent {
	return b.events
}
