# Session Agent Refactoring: Technical Design

**Author:** Crush  
**Date:** 2025-02-26  
**Status:** Draft - Pending Review

---

## 1. Problem Statement

`agent.go` has grown to 1246 lines with `Run()` being a 430-line method containing 10+ inline callbacks. This creates:

- **Cognitive overload**: Hard to understand the flow without reading the entire method
- **Testing gaps**: Integration tests are skipped; unit testing `Run()` is impractical
- **Modification risk**: Changes to one concern (e.g., caching) risk breaking others (e.g., error handling)
- **Hidden state**: Closures capture and mutate variables across callback boundaries

### Current `Run()` Responsibilities (Count: 9)

1. Input validation
2. Queue management (busy check, enqueue, dequeue)
3. User message persistence
4. Async title generation
5. Provider/tool setup with cache markers
6. Streaming orchestration (10 callbacks)
7. Message state tracking (assistant message mutations)
8. Error recovery and partial state persistence
9. Auto-summarization trigger and queue replay

**Principle violated**: Single Responsibility. Each responsibility is a reason to change this method.

---

## 2. Goals

| Goal | Metric |
|------|--------|
| **Testability** | Each new component has >80% unit test coverage |
| **Readability** | No function exceeds 100 lines |
| **Maintainability** | Adding a new callback requires touching 1 file |
| **Backward compatibility** | Zero changes to `SessionAgent` interface |
| **Performance** | No regression in streaming latency |

### Non-Goals

- Changing the `fantasy` library's streaming API
- Refactoring the `coordinator` (separate effort)
- Modifying message/session storage layer

---

## 3. Proposed Architecture

### 3.1 Overview

Split `sessionAgent` into composable components with clear boundaries:

```
┌─────────────────────────────────────────────────────────────┐
│                      SessionAgent                           │
│  (interface unchanged - backward compatible)                │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │ RunContext   │───▶│ StreamRunner │───▶│ StepHandler  │  │
│  │              │    │              │    │              │  │
│  │ - validation │    │ - agent.Stream│   │ - callbacks  │  │
│  │ - queueing   │    │ - loop ctrl  │    │ - mutations  │  │
│  │ - setup      │    │ - completion │    │ - persistence│  │
│  └──────────────┘    └──────────────┘    └──────────────┘  │
│          │                   │                   │          │
│          ▼                   ▼                   ▼          │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                    RunState                          │   │
│  │  (replaces closure-captured variables)               │   │
│  │  - currentAssistant, shouldSummarize, lspBatcher    │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 New Types

#### `RunState` - Explicit State Container

**Problem**: Current implementation uses closure-captured variables (`currentAssistant`, `shouldSummarize`, `lspBatcher`) that are mutated across callbacks. This makes state transitions implicit and hard to trace.

**Solution**: Explicit state struct passed to handlers.

```go
// run_state.go

// RunState holds mutable state for a single Run() invocation.
// All fields are owned by the current goroutine during streaming.
type RunState struct {
    // Immutable after creation
    Call            SessionAgentCall
    Session         session.Session
    Model           Model
    SystemPrompt    string
    PromptPrefix    string
    Tools           []fantasy.AgentTool
    HasSummary      bool
    StartTime       time.Time

    // Mutated during streaming
    CurrentAssistant *message.Message
    ShouldSummarize  bool
    LSPBatcher       *lsp.Batcher

    // Services (immutable references)
    Messages message.Service
    Sessions session.Service
}

// NewRunState creates state from a SessionAgentCall.
func NewRunState(call SessionAgentCall, ...) (*RunState, error)

// Finish cleans up resources (flushes LSP batcher, etc).
func (s *RunState) Finish(ctx context.Context)
```

**Benefit**: State transitions become explicit. Testing can inject mock state.

---

#### `StepHandler` - Callback Router

**Problem**: 10 inline callbacks in `agent.Stream()` create a wall of code. Each callback has similar patterns (update message, persist, handle errors).

**Solution**: Extract callbacks into a handler struct with named methods.

```go
// step_handler.go

// StepHandler processes streaming events from fantasy.Agent.
type StepHandler struct {
    state    *RunState
    messages message.Service
    sessions session.Service
}

func NewStepHandler(state *RunState, msgs message.Service, sess session.Service) *StepHandler

// Reasoning callbacks
func (h *StepHandler) OnReasoningStart(id string, reasoning fantasy.ReasoningContent) error
func (h *StepHandler) OnReasoningDelta(id string, text string) error
func (h *StepHandler) OnReasoningEnd(id string, reasoning fantasy.ReasoningContent) error

// Content callbacks
func (h *StepHandler) OnTextDelta(id string, text string) error

// Tool callbacks
func (h *StepHandler) OnToolInputStart(id string, toolName string) error
func (h *StepHandler) OnToolCall(tc fantasy.ToolCallContent) error
func (h *StepHandler) OnToolResult(result fantasy.ToolResultContent) error

// Lifecycle callbacks
func (h *StepHandler) OnStepFinish(stepResult fantasy.StepResult) error
func (h *StepHandler) OnRetry(err *fantasy.ProviderError, delay time.Duration)

// PrepareStep is special - it creates the assistant message and sets up context.
func (h *StepHandler) PrepareStep(ctx context.Context, opts fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error)
```

**Benefit**: Each callback is independently testable. Adding a new callback is a single method addition.

---

#### `StreamRunner` - Orchestration

**Problem**: `Run()` mixes setup, streaming, and completion handling. The recursive queue-processing call at the end is particularly confusing.

**Solution**: Separate orchestrator that handles the streaming lifecycle.

```go
// stream_runner.go

// StreamRunner executes a single streaming interaction.
type StreamRunner struct {
    agent      fantasy.Agent
    handler    *StepHandler
    state      *RunState
    stopChecks []fantasy.StopCondition
}

func NewStreamRunner(agent fantasy.Agent, handler *StepHandler, state *RunState) *StreamRunner

// Run executes the stream and returns the result.
// Does NOT handle queue processing - that's the caller's responsibility.
func (r *StreamRunner) Run(ctx context.Context) (*fantasy.AgentResult, error)

// buildStreamCall constructs the fantasy.AgentStreamCall with all callbacks wired.
func (r *StreamRunner) buildStreamCall() fantasy.AgentStreamCall
```

**Benefit**: Clear separation between "execute one interaction" and "manage the queue".

---

### 3.3 Refactored `sessionAgent.Run()`

After extracting components, `Run()` becomes a coordinator:

```go
func (a *sessionAgent) Run(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
    // 1. Validate
    if err := validateCall(call); err != nil {
        return nil, err
    }

    // 2. Queue if busy
    if a.IsSessionBusy(call.SessionID) {
        a.enqueue(call)
        return nil, nil
    }

    // 3. Setup state
    state, err := a.buildRunState(ctx, call)
    if err != nil {
        return nil, err
    }
    defer state.Finish(ctx)

    // 4. Async title generation (first message only)
    if state.IsFirstMessage() {
        go a.generateTitle(ctx, call.SessionID, call.Prompt)
    }

    // 5. Create user message
    if _, err := a.createUserMessage(ctx, call); err != nil {
        return nil, err
    }

    // 6. Execute stream
    result, err := a.executeStream(ctx, state)

    // 7. Handle completion (summarization, queue processing)
    return a.handleCompletion(ctx, call, state, result, err)
}

func (a *sessionAgent) executeStream(ctx context.Context, state *RunState) (*fantasy.AgentResult, error) {
    agent := fantasy.NewAgent(state.Model.Model,
        fantasy.WithSystemPrompt(state.SystemPrompt),
        fantasy.WithTools(state.Tools...),
    )

    handler := NewStepHandler(state, a.messages, a.sessions)
    runner := NewStreamRunner(agent, handler, state)

    genCtx, cancel := context.WithCancel(ctx)
    a.activeRequests.Set(state.Call.SessionID, cancel)
    defer a.activeRequests.Del(state.Call.SessionID)
    defer cancel()

    return runner.Run(genCtx)
}

func (a *sessionAgent) handleCompletion(
    ctx context.Context,
    call SessionAgentCall,
    state *RunState,
    result *fantasy.AgentResult,
    err error,
) (*fantasy.AgentResult, error) {
    // Error handling (existing logic, but cleaner)
    if err != nil {
        return a.handleError(ctx, state, err)
    }

    // Auto-summarization
    if state.ShouldSummarize {
        if err := a.triggerSummarization(ctx, call, state); err != nil {
            return nil, err
        }
    }

    // Process queued messages (iterative, not recursive)
    return a.processQueue(ctx, call.SessionID, result)
}
```

**Line count**: ~60 lines vs current ~430 lines.

---

### 3.4 File Structure

```
internal/agent/
├── agent.go              # SessionAgent interface + constructor (slimmed)
├── run_state.go          # RunState struct and helpers
├── step_handler.go       # StepHandler with all callbacks  
├── stream_runner.go      # StreamRunner orchestration
├── error_handler.go      # Error handling extracted from Run()
├── queue.go              # Queue management (enqueue, dequeue, processQueue)
├── summarize.go          # Summarization logic (already partially separate)
├── title.go              # Title generation (already partially separate)
├── cache.go              # applyCacheMarkers and related
├── ... (existing files unchanged)
```

---

## 4. Migration Strategy

### Phase 1: Extract Without Changing Behavior (Low Risk)

1. **Create `RunState`** - Copy closure variables into struct
2. **Create `StepHandler`** - Move callback bodies to methods, keep inline callbacks as one-line delegations
3. **Add tests** for `StepHandler` methods in isolation

```go
// Transitional: inline callbacks delegate to handler
OnTextDelta: func(id string, text string) error {
    return handler.OnTextDelta(id, text)  // One line
},
```

**Verification**: All existing tests pass (once re-recorded). Behavior identical.

### Phase 2: Extract `StreamRunner` (Medium Risk)

1. Move `agent.Stream()` call into `StreamRunner.Run()`
2. Keep error handling in `sessionAgent.Run()` for now
3. Add integration test for `StreamRunner`

**Verification**: Manual testing of streaming behavior. Token counts unchanged.

### Phase 3: Refactor `Run()` Structure (Medium Risk)

1. Split into `executeStream()` and `handleCompletion()`
2. Make queue processing iterative instead of recursive
3. Extract error handling to `handleError()`

**Verification**: Test cancellation, error recovery, queue processing.

### Phase 4: Re-record Test Cassettes (Required)

1. Update VCR cassettes for all model pairs
2. Unskip `TestCoderAgent`
3. Add unit tests for new components

---

## 5. Detailed Changes

### 5.1 `StepHandler` Implementation

```go
// step_handler.go
package agent

import (
    "context"
    "strings"

    "charm.land/fantasy"
    "charm.land/fantasy/providers/anthropic"
    "charm.land/fantasy/providers/google"
    "charm.land/fantasy/providers/openai"
    "github.com/charmbracelet/crush/internal/agent/tools"
    "github.com/charmbracelet/crush/internal/lsp"
    "github.com/charmbracelet/crush/internal/message"
)

// StepHandler processes streaming events and updates message state.
type StepHandler struct {
    state    *RunState
    messages message.Service
}

func NewStepHandler(state *RunState, msgs message.Service) *StepHandler {
    return &StepHandler{state: state, messages: msgs}
}

func (h *StepHandler) OnReasoningStart(id string, reasoning fantasy.ReasoningContent) error {
    h.state.CurrentAssistant.AppendReasoningContent(reasoning.Text)
    return h.messages.Update(h.state.Ctx, *h.state.CurrentAssistant)
}

func (h *StepHandler) OnReasoningDelta(id string, text string) error {
    h.state.CurrentAssistant.AppendReasoningContent(text)
    return h.messages.Update(h.state.Ctx, *h.state.CurrentAssistant)
}

func (h *StepHandler) OnReasoningEnd(id string, reasoning fantasy.ReasoningContent) error {
    h.extractReasoningSignatures(reasoning)
    h.state.CurrentAssistant.FinishThinking()
    return h.messages.Update(h.state.Ctx, *h.state.CurrentAssistant)
}

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

func (h *StepHandler) OnTextDelta(id string, text string) error {
    // Strip leading newline from initial text content.
    if len(h.state.CurrentAssistant.Parts) == 0 {
        text = strings.TrimPrefix(text, "\n")
    }
    h.state.CurrentAssistant.AppendContent(text)
    return h.messages.Update(h.state.Ctx, *h.state.CurrentAssistant)
}

func (h *StepHandler) OnToolInputStart(id string, toolName string) error {
    toolCall := message.ToolCall{
        ID:               id,
        Name:             toolName,
        ProviderExecuted: false,
        Finished:         false,
    }
    h.state.CurrentAssistant.AddToolCall(toolCall)
    return h.messages.Update(h.state.Ctx, *h.state.CurrentAssistant)
}

func (h *StepHandler) OnToolCall(tc fantasy.ToolCallContent) error {
    toolCall := message.ToolCall{
        ID:               tc.ToolCallID,
        Name:             tc.ToolName,
        Input:            tc.Input,
        ProviderExecuted: false,
        Finished:         true,
    }
    h.state.CurrentAssistant.AddToolCall(toolCall)
    return h.messages.Update(h.state.Ctx, *h.state.CurrentAssistant)
}

func (h *StepHandler) OnToolResult(result fantasy.ToolResultContent) error {
    toolResult := convertToToolResult(result)
    _, err := h.messages.Create(h.state.Ctx, h.state.CurrentAssistant.SessionID, message.CreateMessageParams{
        Role:  message.Tool,
        Parts: []message.ContentPart{toolResult},
    })
    return err
}

// PrepareStep creates the assistant message and injects context values.
func (h *StepHandler) PrepareStep(
    ctx context.Context,
    opts fantasy.PrepareStepFunctionOptions,
) (context.Context, fantasy.PrepareStepResult, error) {
    prepared := fantasy.PrepareStepResult{Messages: opts.Messages}

    // Clear provider options from messages.
    for i := range prepared.Messages {
        prepared.Messages[i].ProviderOptions = nil
    }

    // Process queued calls (existing logic).
    prepared.Messages = h.processQueuedMessages(ctx, prepared.Messages)

    // Provider-specific transformations.
    prepared.Messages = h.state.TransformMessages(prepared.Messages)

    // Deduplicate and apply cache markers.
    DedupeToolOutputs(prepared.Messages)
    prepared.Messages = h.injectSystemMessages(prepared.Messages)
    applyCacheMarkers(prepared.Messages, h.state.HasSummary, h.state.CacheOptions)

    // Create assistant message.
    assistantMsg, err := h.createAssistantMessage(ctx)
    if err != nil {
        return ctx, prepared, err
    }

    // Inject context values.
    ctx = context.WithValue(ctx, tools.MessageIDContextKey, assistantMsg.ID)
    ctx = context.WithValue(ctx, tools.SupportsImagesContextKey, h.state.Model.CatwalkCfg.SupportsImages)
    ctx = context.WithValue(ctx, tools.ModelNameContextKey, h.state.Model.CatwalkCfg.Name)

    // Create LSP batcher.
    if h.state.LSPManager != nil {
        h.state.LSPBatcher = lsp.NewBatcher(h.state.LSPManager)
        ctx = tools.WithLSPBatcher(ctx, h.state.LSPBatcher)
    }

    h.state.CurrentAssistant = &assistantMsg
    return ctx, prepared, nil
}

func (h *StepHandler) createAssistantMessage(ctx context.Context) (message.Message, error) {
    return h.messages.Create(ctx, h.state.Call.SessionID, message.CreateMessageParams{
        Role:     message.Assistant,
        Parts:    []message.ContentPart{},
        Model:    h.state.Model.ModelCfg.Model,
        Provider: h.state.Model.ModelCfg.Provider,
    })
}

func (h *StepHandler) injectSystemMessages(msgs []fantasy.Message) []fantasy.Message {
    if h.state.Call.TemplateContext != "" {
        msgs = append([]fantasy.Message{
            fantasy.NewSystemMessage(h.state.Call.TemplateContext),
        }, msgs...)
    }
    if h.state.PromptPrefix != "" {
        msgs = append([]fantasy.Message{
            fantasy.NewSystemMessage(h.state.PromptPrefix),
        }, msgs...)
    }
    return msgs
}

func (h *StepHandler) processQueuedMessages(ctx context.Context, msgs []fantasy.Message) []fantasy.Message {
    // Existing queue processing logic - moved from inline callback.
    // (Implementation details omitted for brevity - copy from existing code)
    return msgs
}
```

### 5.2 Testing Strategy

```go
// step_handler_test.go
package agent

import (
    "testing"

    "charm.land/fantasy"
    "github.com/charmbracelet/crush/internal/message"
    "github.com/stretchr/testify/require"
)

func TestStepHandler_OnTextDelta(t *testing.T) {
    t.Parallel()

    t.Run("strips leading newline on first content", func(t *testing.T) {
        t.Parallel()
        msgs := &fakeMessageService{}
        state := &RunState{
            CurrentAssistant: &message.Message{Parts: []message.ContentPart{}},
        }
        handler := NewStepHandler(state, msgs)

        err := handler.OnTextDelta("id", "\nHello")
        require.NoError(t, err)
        require.Equal(t, "Hello", state.CurrentAssistant.Content().Text)
    })

    t.Run("preserves newlines after first content", func(t *testing.T) {
        t.Parallel()
        msgs := &fakeMessageService{}
        state := &RunState{
            CurrentAssistant: &message.Message{
                Parts: []message.ContentPart{message.TextContent{Text: "First"}},
            },
        }
        handler := NewStepHandler(state, msgs)

        err := handler.OnTextDelta("id", "\nSecond")
        require.NoError(t, err)
        require.Contains(t, state.CurrentAssistant.Content().Text, "\nSecond")
    })
}

func TestStepHandler_OnReasoningEnd(t *testing.T) {
    t.Parallel()

    t.Run("extracts anthropic signature", func(t *testing.T) {
        t.Parallel()
        // Test provider-specific metadata extraction
    })

    t.Run("finishes thinking state", func(t *testing.T) {
        t.Parallel()
        // Verify FinishThinking() is called
    })
}

func TestStepHandler_PrepareStep(t *testing.T) {
    t.Parallel()

    t.Run("injects template context as first system message", func(t *testing.T) {
        t.Parallel()
        // Test system message ordering
    })

    t.Run("applies cache markers correctly", func(t *testing.T) {
        t.Parallel()
        // Test cache marker placement
    })

    t.Run("creates assistant message", func(t *testing.T) {
        t.Parallel()
        // Verify message creation
    })
}
```

---

## 6. Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Behavioral regression in streaming | Medium | High | Phase 1 keeps callbacks as delegations; diff testing |
| State race conditions | Low | High | `RunState` is goroutine-local; no sharing |
| Performance regression | Low | Medium | Benchmark before/after; no new allocations in hot path |
| Test cassette drift | High | Medium | Re-record immediately after Phase 1 |
| Integration with fantasy changes | Low | Low | Interface is stable; we're only restructuring our side |

---

## 7. Success Criteria

1. **All tests pass** (including re-recorded cassettes)
2. **No function exceeds 100 lines** in new files
3. **`StepHandler` has >90% coverage** for callback methods
4. **`Run()` is under 80 lines** after refactor
5. **No behavioral changes** detectable in manual testing
6. **Code review approval** from team

---

## 8. Open Questions

1. **Should `StepHandler` be an interface?** 
   - Pro: Easier mocking in tests
   - Con: More indirection, YAGNI for now
   - **Recommendation**: Start concrete, extract interface if needed

2. **Should we use a state machine for the streaming loop?**
   - Pro: More explicit state transitions
   - Con: Over-engineering for current complexity
   - **Recommendation**: Defer unless we add more states

3. **Where should error handling live?**
   - Option A: In `StepHandler` (each callback handles its errors)
   - Option B: In `StreamRunner` (centralized error handling)
   - **Recommendation**: Option B - matches current pattern, easier to reason about

4. **Should queue processing remain in `sessionAgent` or move to a separate `QueueManager`?**
   - **Recommendation**: Keep in `sessionAgent` for now; it's only ~50 lines

---

## 9. Timeline Estimate

| Phase | Effort | Dependencies |
|-------|--------|--------------|
| Phase 1: Extract types | 2-3 hours | None |
| Phase 2: Extract StreamRunner | 1-2 hours | Phase 1 |
| Phase 3: Refactor Run() | 1-2 hours | Phase 2 |
| Phase 4: Re-record cassettes | 2-4 hours | Phase 3 + API keys |
| Testing & review | 2-3 hours | Phase 4 |

**Total: ~10-14 hours of focused work**

---

## 10. Appendix: Current vs Proposed Line Counts

| File | Current | Proposed |
|------|---------|----------|
| agent.go | 1246 | ~400 |
| run_state.go | - | ~80 |
| step_handler.go | - | ~200 |
| stream_runner.go | - | ~100 |
| error_handler.go | - | ~150 |
| queue.go | - | ~60 |

**Net change**: ~1246 → ~990 lines across 6 files (20% reduction + much better organization)

---

## 11. Testing Infrastructure (Implemented)

### ScriptedAgent

A deterministic mock streaming provider that emits scripted events through the same callback interface used by `fantasy.AgentStreamCall`. This enables precise testing of orchestration behavior without API calls.

**Location**: `internal/agent/testutil_test.go`

**Key types**:
- `StreamEvent` - Interface for all scriptable events
- `ScriptedAgent` - Implements `fantasy.Agent` with predetermined script
- `ScriptBuilder` - Fluent API for building test scripts

**Supported events**:
- `TextDeltaEvent` - Text content streaming
- `ReasoningStartEvent`, `ReasoningDeltaEvent`, `ReasoningEndEvent` - Reasoning/thinking
- `ToolInputStartEvent`, `ToolCallEvent`, `ToolResultEvent` - Tool lifecycle
- `ErrorEvent` - Provider errors
- `CancelEvent` - Context cancellation (immediately returns `context.Canceled`)
- `FinishEvent` - Step completion with finish reason
- `StepBoundaryEvent` - Multi-step interaction boundaries

### Golden Tests

**Location**: `internal/agent/golden_test.go`

| Test | Validates |
|------|-----------|
| `TestGolden_CancelMidToolCall` | Tool calls force-closed on cancel, finish reason set |
| `TestGolden_ToolFailure` | Tool error results persisted correctly |
| `TestGolden_ProviderError` | Provider errors set finish reason and details |
| `TestGolden_NormalCompletion` | Happy path - text streaming, finish reason |
| `TestGolden_ReasoningThenResponse` | Reasoning content captured before response |
| `TestGolden_MultiStepToolUse` | Multi-step tool interactions create correct message sequence |
| `TestGolden_CancelDuringReasoning` | Reasoning saved even on cancel |

### Usage Example

```go
script := NewScript().
    ToolStart("tool-1", "bash").
    Cancel().  // Cancel before tool finishes
    Build()

scriptedAgent := NewScriptedAgent(script)
ctx, cancel := context.WithCancel(t.Context())
scriptedAgent.SetCancelFunc(cancel)

// Run and verify error handling behavior
_, err := agent.Run(ctx, call)
require.ErrorIs(t, err, context.Canceled)
```

### Key Design Decision: Background Context for Error Recovery

The `scriptedSessionAgent` uses `context.Background()` for the final message update during error recovery. This mirrors the real agent's use of parent context, ensuring DB writes complete even after cancellation.

```go
// In error handler:
_ = a.messages.Update(context.Background(), *currentAssistant)
```
