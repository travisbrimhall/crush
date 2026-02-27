# Agent Refactoring Status

## Completed (Phases 1-4)

**Goal**: Reduce `Run()` method from 430 lines with 10+ inline callbacks to testable components.

### Architecture (Three-Layer Pattern)
1. **StreamRunner** (`stream_runner.go`, 175 lines) - Orchestration: initiates streaming, wires callbacks/stop conditions, separates `parentCtx` from `streamCtx`
2. **StepHandler** (`step_handler.go`, 254 lines) - Callbacks: `OnReasoningStart`, `OnReasoningDelta`, `OnReasoningEnd`, `OnTextDelta`, `OnToolInputStart`, `OnToolCall`, `OnToolResult`, `OnRetry`, `OnStepFinish`, `PrepareStep`
3. **RunState** (`run_state.go`, 107 lines) - State: single source of truth, single-use per `Run()` invocation

### Other Files
- `error_handler.go` (194 lines) - Error handling logic
- `golden_test.go` - 7 golden tests for orchestration invariants
- `DESIGN.md` - Technical design document

### Result
- `Run()` reduced to ~95 lines
- All tests passing: `go test ./internal/agent/...`

## Future Work (Deferred)

1. **Unit tests for StepHandler** - Test individual callback methods:
   - `OnTextDelta` strips leading newlines on first content
   - `OnReasoningEnd` extracts provider-specific signatures
   - `PrepareStep` injects system messages in correct order

2. **Queue iteration refactor** (optional):
   - Convert recursive `return a.Run(ctx, firstQueuedMessage)` to iterative loop

3. **Re-record VCR cassettes** (requires API keys):
   - Unskip `TestCoderAgent` in `agent_test.go:50`

## Key Invariants
- `AddToolCall` deduplicates by ID (replaces existing)
- `AddFinish` replaces existing finish
- LSP flush uses `streamCtx`
- Queue delete-before-process
- `MaxOutputTokens` pointer handling preserved
