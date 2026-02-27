package agent

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// TestGolden_CancelMidToolCall verifies behavior when cancellation occurs
// after a tool call starts but before it finishes.
func TestGolden_CancelMidToolCall(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	script := NewScript().
		ToolStart("tool-1", "bash").
		Cancel(). // Cancel before tool finishes
		Build()

	scriptedAgent := NewScriptedAgent(script)

	// Create a cancellable context and wire it to the agent.
	ctx, cancel := context.WithCancel(t.Context())
	scriptedAgent.SetCancelFunc(cancel)

	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	_, err = agent.Run(ctx, SessionAgentCall{
		Prompt:          "test prompt",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	// Should return context.Canceled error.
	require.ErrorIs(t, err, context.Canceled)

	// Verify message state.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	// Should have: user message, assistant message.
	require.GreaterOrEqual(t, len(msgs), 2)

	// Find assistant message.
	var assistantMsg *message.Message
	for i := range msgs {
		if msgs[i].Role == message.Assistant {
			assistantMsg = &msgs[i]
			break
		}
	}
	require.NotNil(t, assistantMsg, "expected assistant message")

	// Tool call should be force-finished.
	toolCalls := assistantMsg.ToolCalls()
	require.Len(t, toolCalls, 1)
	require.True(t, toolCalls[0].Finished, "tool call should be marked finished on cancel")
	require.Equal(t, "{}", toolCalls[0].Input, "tool call input should be empty JSON on cancel")

	// Finish reason should indicate cancellation.
	finish := assistantMsg.FinishPart()
	require.Equal(t, message.FinishReasonCanceled, finish.Reason)
}

// TestGolden_ToolFailure verifies behavior when a tool returns an error.
func TestGolden_ToolFailure(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	script := NewScript().
		ToolStart("tool-1", "bash").
		ToolCall("tool-1", "bash", `{"command": "exit 1"}`).
		ToolError("tool-1", "bash", "command failed with exit code 1").
		FinishToolUse().
		StepBoundary().
		TextDelta("The command failed.").
		Finish().
		Build()

	scriptedAgent := NewScriptedAgent(script)
	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	result, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          "run a failing command",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify messages.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	// Should have: user, assistant (with tool call), tool result, assistant (with response).
	require.GreaterOrEqual(t, len(msgs), 3)

	// Find tool result message.
	var toolResultMsg *message.Message
	for i := range msgs {
		if msgs[i].Role == message.Tool {
			toolResultMsg = &msgs[i]
			break
		}
	}
	require.NotNil(t, toolResultMsg, "expected tool result message")

	toolResults := toolResultMsg.ToolResults()
	require.Len(t, toolResults, 1)
	require.True(t, toolResults[0].IsError)
	require.Contains(t, toolResults[0].Content, "command failed")
}

// TestGolden_ProviderError verifies behavior when the provider returns an error.
func TestGolden_ProviderError(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	providerErr := &fantasy.ProviderError{
		Title:      "rate_limit_exceeded",
		Message:    "Too many requests",
		StatusCode: 429,
	}

	script := NewScript().
		TextDelta("Starting to "). // Partial response before error
		Error(providerErr).
		Build()

	scriptedAgent := NewScriptedAgent(script)
	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	_, err = agent.Run(t.Context(), SessionAgentCall{
		Prompt:          "test prompt",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	// Should return the provider error.
	require.Error(t, err)
	var gotProviderErr *fantasy.ProviderError
	require.True(t, errors.As(err, &gotProviderErr))
	require.Equal(t, 429, gotProviderErr.StatusCode)

	// Verify assistant message has error finish reason.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var assistantMsg *message.Message
	for i := range msgs {
		if msgs[i].Role == message.Assistant {
			assistantMsg = &msgs[i]
		}
	}
	require.NotNil(t, assistantMsg)

	finish := assistantMsg.FinishPart()
	require.Equal(t, message.FinishReasonError, finish.Reason)
	require.Contains(t, finish.Details, "Too many requests")
}

// TestGolden_NormalCompletion verifies the happy path works correctly.
func TestGolden_NormalCompletion(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	script := NewScript().
		TextDelta("Hello, ").
		TextDelta("world!").
		Finish().
		Build()

	scriptedAgent := NewScriptedAgent(script)
	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	result, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          "say hello",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify messages.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	// Should have: user message, assistant message.
	require.Len(t, msgs, 2)

	require.Equal(t, message.User, msgs[0].Role)
	require.Equal(t, message.Assistant, msgs[1].Role)

	// Check assistant content.
	content := msgs[1].Content()
	require.Equal(t, "Hello, world!", content.Text)

	// Check finish reason.
	finish := msgs[1].FinishPart()
	require.Equal(t, message.FinishReasonEndTurn, finish.Reason)
}

// TestGolden_ReasoningThenResponse verifies reasoning content is captured.
func TestGolden_ReasoningThenResponse(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	script := NewScript().
		ReasoningStart("Let me think...").
		ReasoningDelta(" I should say hello.").
		ReasoningEnd().
		TextDelta("Hello!").
		Finish().
		Build()

	scriptedAgent := NewScriptedAgent(script)
	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	result, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          "think and respond",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var assistantMsg *message.Message
	for i := range msgs {
		if msgs[i].Role == message.Assistant {
			assistantMsg = &msgs[i]
		}
	}
	require.NotNil(t, assistantMsg)

	// Check reasoning content was captured.
	reasoning := assistantMsg.ReasoningContent()
	require.Contains(t, reasoning.String(), "Let me think...")
	require.Contains(t, reasoning.String(), "I should say hello.")

	// Check response content.
	content := assistantMsg.Content()
	require.Equal(t, "Hello!", content.Text)
}

// TestGolden_MultiStepToolUse verifies multi-step tool interactions.
func TestGolden_MultiStepToolUse(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	script := NewScript().
		// Step 1: Tool call.
		ToolStart("tool-1", "view").
		ToolCall("tool-1", "view", `{"file_path": "/test.txt"}`).
		ToolResult("tool-1", "view", "file contents here").
		FinishToolUse().
		StepBoundary().
		// Step 2: Response based on tool result.
		TextDelta("The file contains: file contents here").
		Finish().
		Build()

	scriptedAgent := NewScriptedAgent(script)
	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	result, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          "read the file",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	// Should have: user, assistant (tool call), tool result, assistant (response).
	require.GreaterOrEqual(t, len(msgs), 4)

	// Verify we have both assistant messages.
	assistantCount := 0
	for _, msg := range msgs {
		if msg.Role == message.Assistant {
			assistantCount++
		}
	}
	require.Equal(t, 2, assistantCount, "expected 2 assistant messages for multi-step")

	// Last assistant message should have the final response.
	lastAssistant := msgs[len(msgs)-1]
	require.Equal(t, message.Assistant, lastAssistant.Role)
	require.Contains(t, lastAssistant.Content().Text, "file contents here")
}

// TestGolden_CancelDuringReasoning verifies cancellation during reasoning.
func TestGolden_CancelDuringReasoning(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test Session", "")
	require.NoError(t, err)

	script := NewScript().
		ReasoningStart("Thinking...").
		ReasoningDelta(" more thoughts").
		Cancel(). // Cancel mid-reasoning
		Build()

	scriptedAgent := NewScriptedAgent(script)

	ctx, cancel := context.WithCancel(t.Context())
	scriptedAgent.SetCancelFunc(cancel)

	agent := testSessionAgentWithScriptedAgent(env, scriptedAgent)

	_, err = agent.Run(ctx, SessionAgentCall{
		Prompt:          "think deeply",
		SessionID:       sess.ID,
		MaxOutputTokens: 1000,
	})

	require.ErrorIs(t, err, context.Canceled)

	// Verify reasoning was saved and thinking was finished.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var assistantMsg *message.Message
	for i := range msgs {
		if msgs[i].Role == message.Assistant {
			assistantMsg = &msgs[i]
		}
	}
	require.NotNil(t, assistantMsg)

	// Reasoning should be captured even on cancel.
	reasoning := assistantMsg.ReasoningContent()
	require.Contains(t, reasoning.String(), "Thinking...")
}

// testSessionAgentWithScriptedAgent creates a sessionAgent that uses a
// ScriptedAgent instead of a real fantasy.Agent.
func testSessionAgentWithScriptedAgent(env fakeEnv, scripted *ScriptedAgent) SessionAgent {
	// Create a wrapper that implements the same interface.
	// We need to create a real sessionAgent but inject our scripted behavior.
	// For now, we'll create a minimal sessionAgent.
	return &scriptedSessionAgent{
		scripted:         scripted,
		sessions:         env.sessions,
		messages:         env.messages,
		activeRequests:   make(map[string]context.CancelFunc),
		disableTitleGen:  true,
		disableSummarize: true,
	}
}

// scriptedSessionAgent wraps ScriptedAgent to implement SessionAgent interface.
type scriptedSessionAgent struct {
	scripted         *ScriptedAgent
	sessions         session.Service
	messages         message.Service
	activeRequests   map[string]context.CancelFunc
	disableTitleGen  bool
	disableSummarize bool
}

func (a *scriptedSessionAgent) Run(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
	if call.Prompt == "" {
		return nil, ErrEmptyPrompt
	}
	if call.SessionID == "" {
		return nil, ErrSessionMissing
	}

	// Create user message.
	_, err := a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: call.Prompt}},
	})
	if err != nil {
		return nil, err
	}

	// Track cancellation.
	genCtx, cancel := context.WithCancel(ctx)
	a.activeRequests[call.SessionID] = cancel
	defer delete(a.activeRequests, call.SessionID)
	defer cancel()

	// Track current assistant message.
	var currentAssistant *message.Message

	// Build the stream call with callbacks that mirror real sessionAgent.
	streamCall := fantasy.AgentStreamCall{
		Prompt: call.Prompt,
		PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
			assistantMsg, createErr := a.messages.Create(callCtx, call.SessionID, message.CreateMessageParams{
				Role:  message.Assistant,
				Parts: []message.ContentPart{},
			})
			if createErr != nil {
				return callCtx, fantasy.PrepareStepResult{}, createErr
			}
			currentAssistant = &assistantMsg
			return callCtx, fantasy.PrepareStepResult{Messages: opts.Messages}, nil
		},
		OnReasoningStart: func(id string, reasoning fantasy.ReasoningContent) error {
			currentAssistant.AppendReasoningContent(reasoning.Text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningDelta: func(id string, text string) error {
			currentAssistant.AppendReasoningContent(text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			currentAssistant.FinishThinking()
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnTextDelta: func(id string, text string) error {
			currentAssistant.AppendContent(text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnToolInputStart: func(id string, toolName string) error {
			toolCall := message.ToolCall{
				ID:       id,
				Name:     toolName,
				Finished: false,
			}
			currentAssistant.AddToolCall(toolCall)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			toolCall := message.ToolCall{
				ID:       tc.ToolCallID,
				Name:     tc.ToolName,
				Input:    tc.Input,
				Finished: true,
			}
			currentAssistant.AddToolCall(toolCall)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			tr := message.ToolResult{
				ToolCallID: result.ToolCallID,
				Name:       result.ToolName,
			}
			if text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Result); ok {
				tr.Content = text.Text
			}
			if errResult, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](result.Result); ok {
				tr.Content = errResult.Error.Error()
				tr.IsError = true
			}
			_, createErr := a.messages.Create(genCtx, call.SessionID, message.CreateMessageParams{
				Role:  message.Tool,
				Parts: []message.ContentPart{tr},
			})
			return createErr
		},
		OnStepFinish: func(stepResult fantasy.StepResult) error {
			finishReason := message.FinishReasonUnknown
			switch stepResult.FinishReason {
			case fantasy.FinishReasonStop:
				finishReason = message.FinishReasonEndTurn
			case fantasy.FinishReasonToolCalls:
				finishReason = message.FinishReasonToolUse
			}
			currentAssistant.AddFinish(finishReason, "", "")
			return a.messages.Update(genCtx, *currentAssistant)
		},
	}

	result, err := a.scripted.Stream(genCtx, streamCall)

	// Handle errors - mirror real sessionAgent behavior.
	if err != nil {
		if currentAssistant != nil {
			currentAssistant.FinishThinking()
			// Force-close unfinished tool calls.
			for _, tc := range currentAssistant.ToolCalls() {
				if !tc.Finished {
					tc.Finished = true
					tc.Input = "{}"
					currentAssistant.AddToolCall(tc)
				}
			}

			// Set appropriate finish reason.
			if errors.Is(err, context.Canceled) {
				currentAssistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
			} else {
				var providerErr *fantasy.ProviderError
				if errors.As(err, &providerErr) {
					currentAssistant.AddFinish(message.FinishReasonError, providerErr.Title, providerErr.Message)
				} else {
					currentAssistant.AddFinish(message.FinishReasonError, "Error", err.Error())
				}
			}
			// Use background context for final update since ctx may be cancelled.
			_ = a.messages.Update(context.Background(), *currentAssistant)
		}
		return nil, err
	}

	return result, nil
}

func (a *scriptedSessionAgent) SetModels(large Model, small Model)          {}
func (a *scriptedSessionAgent) SetTools(tools []fantasy.AgentTool)          {}
func (a *scriptedSessionAgent) SetSystemPrompt(systemPrompt string)         {}
func (a *scriptedSessionAgent) Cancel(sessionID string)                     {}
func (a *scriptedSessionAgent) CancelAll()                                  {}
func (a *scriptedSessionAgent) IsSessionBusy(sessionID string) bool         { return false }
func (a *scriptedSessionAgent) IsBusy() bool                                { return false }
func (a *scriptedSessionAgent) QueuedPrompts(sessionID string) int          { return 0 }
func (a *scriptedSessionAgent) QueuedPromptsList(sessionID string) []string { return nil }
func (a *scriptedSessionAgent) ClearQueue(sessionID string)                 {}
func (a *scriptedSessionAgent) Model() Model                                { return Model{} }
func (a *scriptedSessionAgent) Summarize(ctx context.Context, sessionID string, opts fantasy.ProviderOptions) (string, error) {
	return "", nil
}
