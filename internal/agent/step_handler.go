package agent

import (
	"context"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// StepHandler processes streaming events from fantasy.Agent.
// It encapsulates all callback logic for a single Run() invocation.
type StepHandler struct {
	state      *RunState
	agent      *sessionAgent
	messages   message.Service
	sessions   session.Service
	lspManager *lsp.Manager
	streamCtx  context.Context // Context for streaming operations (may be cancelled).
}

// NewStepHandler creates a handler for the given run state.
func NewStepHandler(
	state *RunState,
	agent *sessionAgent,
	msgs message.Service,
	sess session.Service,
	lspManager *lsp.Manager,
) *StepHandler {
	return &StepHandler{
		state:      state,
		agent:      agent,
		messages:   msgs,
		sessions:   sess,
		lspManager: lspManager,
	}
}

// SetStreamContext sets the context for streaming operations.
// This should be called before streaming begins.
func (h *StepHandler) SetStreamContext(ctx context.Context) {
	h.streamCtx = ctx
}

// OnReasoningStart handles the start of reasoning content.
func (h *StepHandler) OnReasoningStart(id string, reasoning fantasy.ReasoningContent) error {
	h.state.CurrentAssistant.AppendReasoningContent(reasoning.Text)
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// OnReasoningDelta handles incremental reasoning content.
func (h *StepHandler) OnReasoningDelta(id string, text string) error {
	h.state.CurrentAssistant.AppendReasoningContent(text)
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// OnReasoningEnd handles the completion of reasoning content.
// It extracts provider-specific signatures and finishes the thinking state.
func (h *StepHandler) OnReasoningEnd(id string, reasoning fantasy.ReasoningContent) error {
	h.extractReasoningSignatures(reasoning)
	h.state.CurrentAssistant.FinishThinking()
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// extractReasoningSignatures extracts provider-specific reasoning signatures.
func (h *StepHandler) extractReasoningSignatures(reasoning fantasy.ReasoningContent) {
	if data, ok := reasoning.ProviderMetadata[anthropic.Name]; ok {
		if r, ok := data.(*anthropic.ReasoningOptionMetadata); ok {
			h.state.CurrentAssistant.AppendReasoningSignature(r.Signature)
		}
	}
	if data, ok := reasoning.ProviderMetadata[google.Name]; ok {
		if r, ok := data.(*google.ReasoningMetadata); ok {
			h.state.CurrentAssistant.AppendThoughtSignature(r.Signature, r.ToolID)
		}
	}
	if data, ok := reasoning.ProviderMetadata[openai.Name]; ok {
		if r, ok := data.(*openai.ResponsesReasoningMetadata); ok {
			h.state.CurrentAssistant.SetReasoningResponsesData(r)
		}
	}
}

// OnTextDelta handles incremental text content.
// Strips leading newline from initial text content.
func (h *StepHandler) OnTextDelta(id string, text string) error {
	// Strip leading newline from initial text content. This is particularly
	// important in non-interactive mode where leading newlines are very visible.
	if len(h.state.CurrentAssistant.Parts) == 0 {
		text = strings.TrimPrefix(text, "\n")
	}
	h.state.CurrentAssistant.AppendContent(text)
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// OnToolInputStart handles the start of tool input streaming.
func (h *StepHandler) OnToolInputStart(id string, toolName string) error {
	toolCall := message.ToolCall{
		ID:               id,
		Name:             toolName,
		ProviderExecuted: false,
		Finished:         false,
	}
	h.state.CurrentAssistant.AddToolCall(toolCall)
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// OnToolCall handles a completed tool call.
func (h *StepHandler) OnToolCall(tc fantasy.ToolCallContent) error {
	toolCall := message.ToolCall{
		ID:               tc.ToolCallID,
		Name:             tc.ToolName,
		Input:            tc.Input,
		ProviderExecuted: false,
		Finished:         true,
	}
	h.state.CurrentAssistant.AddToolCall(toolCall)
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// OnToolResult handles a tool result.
func (h *StepHandler) OnToolResult(result fantasy.ToolResultContent) error {
	toolResult := h.agent.convertToToolResult(result)
	_, err := h.messages.Create(h.streamCtx, h.state.CurrentAssistant.SessionID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{toolResult},
	})
	return err
}

// OnRetry handles retry events from the provider.
func (h *StepHandler) OnRetry(err *fantasy.ProviderError, delay time.Duration) {
	// TODO: implement retry handling (emit event, log, etc.)
}

// OnStepFinish handles the completion of a streaming step.
// It flushes the LSP batcher, updates the assistant message finish reason,
// and persists session usage.
func (h *StepHandler) OnStepFinish(ctx context.Context, stepResult fantasy.StepResult) error {
	// Flush LSP batcher to notify all files and wait for diagnostics once.
	if h.state.LSPBatcher != nil {
		h.state.LSPBatcher.Flush(h.streamCtx)
		h.state.LSPBatcher = nil
	}

	finishReason := h.mapFinishReason(stepResult.FinishReason)
	h.state.CurrentAssistant.AddFinish(finishReason, "", "")

	h.state.SessionLock.Lock()
	defer h.state.SessionLock.Unlock()

	updatedSession, err := h.sessions.Get(ctx, h.state.Call.SessionID)
	if err != nil {
		return err
	}
	h.agent.updateSessionUsage(h.state.Model, &updatedSession, stepResult.Usage, h.agent.openrouterCost(stepResult.ProviderMetadata))
	_, err = h.sessions.Save(ctx, updatedSession)
	if err != nil {
		return err
	}
	h.state.Session = updatedSession
	return h.messages.Update(h.streamCtx, *h.state.CurrentAssistant)
}

// mapFinishReason converts fantasy.FinishReason to message.FinishReason.
func (h *StepHandler) mapFinishReason(reason fantasy.FinishReason) message.FinishReason {
	switch reason {
	case fantasy.FinishReasonLength:
		return message.FinishReasonMaxTokens
	case fantasy.FinishReasonStop:
		return message.FinishReasonEndTurn
	case fantasy.FinishReasonToolCalls:
		return message.FinishReasonToolUse
	default:
		return message.FinishReasonUnknown
	}
}

// PrepareStep prepares the context and messages for a streaming step.
// It processes queued messages, applies transformations, creates the
// assistant message, and injects context values.
func (h *StepHandler) PrepareStep(
	callContext context.Context,
	options fantasy.PrepareStepFunctionOptions,
) (context.Context, fantasy.PrepareStepResult, error) {
	prepared := fantasy.PrepareStepResult{Messages: options.Messages}

	// Clear provider options from messages.
	for i := range prepared.Messages {
		prepared.Messages[i].ProviderOptions = nil
	}

	// Process queued calls.
	queuedCalls, _ := h.agent.messageQueue.Get(h.state.Call.SessionID)
	h.agent.messageQueue.Del(h.state.Call.SessionID)
	for _, queued := range queuedCalls {
		userMessage, err := h.agent.createUserMessage(callContext, queued)
		if err != nil {
			return callContext, prepared, err
		}
		prepared.Messages = append(prepared.Messages, userMessage.ToAIMessage()...)
	}

	// Provider-specific transformations.
	prepared.Messages = h.agent.workaroundProviderMediaLimitations(prepared.Messages, h.state.Model)

	// Deduplicate repeated file contents to reduce token usage.
	DedupeToolOutputs(prepared.Messages)

	// Inject template context as system message (cacheable).
	if h.state.Call.TemplateContext != "" {
		prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(h.state.Call.TemplateContext)}, prepared.Messages...)
	}

	if h.state.PromptPrefix != "" {
		prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(h.state.PromptPrefix)}, prepared.Messages...)
	}

	// Apply cache control markers AFTER all messages are assembled.
	applyCacheMarkers(prepared.Messages, h.state.HasSummary, h.agent.getCacheControlOptions())

	// Create assistant message.
	assistantMsg, err := h.messages.Create(callContext, h.state.Call.SessionID, message.CreateMessageParams{
		Role:     message.Assistant,
		Parts:    []message.ContentPart{},
		Model:    h.state.Model.ModelCfg.Model,
		Provider: h.state.Model.ModelCfg.Provider,
	})
	if err != nil {
		return callContext, prepared, err
	}

	// Inject context values.
	callContext = context.WithValue(callContext, tools.MessageIDContextKey, assistantMsg.ID)
	callContext = context.WithValue(callContext, tools.SupportsImagesContextKey, h.state.Model.CatwalkCfg.SupportsImages)
	callContext = context.WithValue(callContext, tools.ModelNameContextKey, h.state.Model.CatwalkCfg.Name)

	// Create LSP batcher for this step to batch file notifications.
	if h.lspManager != nil {
		h.state.LSPBatcher = lsp.NewBatcher(h.lspManager)
		callContext = tools.WithLSPBatcher(callContext, h.state.LSPBatcher)
	}

	h.state.CurrentAssistant = &assistantMsg
	return callContext, prepared, nil
}
