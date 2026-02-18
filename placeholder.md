# Tidy Feature - Expanded Compression

## Summary of Changes

Expanded compression from tool_results only → all content types:

| Content Type | Key Format | What Gets Compressed |
|-------------|-----------|---------------------|
| `tool_result` | `tool_call_id` | File views, command output, search results |
| `tool_call` | `tool_call_id` | Tool input params (long URLs, code blocks) |
| `user_text` | `msg_{idx}_{block}` | User messages, pastes, error logs |
| `assistant` | `msg_{idx}_{block}` | Assistant responses, explanations |

### Key Implementation Details
- Structure preserved: Tool call IDs, tool names, metadata all remain intact
- JSON response format now uses `id` field instead of `tool_call_id`
- Added lifecycle logging throughout

### Files Changed
- `internal/agent/tidy.go`: New `tidyContentType` enum, expanded `FindTidyCandidates`, updated `ApplyTidyCompressions`
- `internal/agent/templates/tidy.md`: Updated prompt with content type guidance
- `internal/agent/agent.go`: Simplified `ParseTidyResponse` caller

## Conditions for Compression

- Content must be **6+ messages old** (`tidyMinMessageAge`)
- Content must be **500+ chars** (`tidyMinContentSize`)
- Content must **not already be compressed** (no `<investigation>`, `<summary>`, `<compressed>`, `<file_map>` tags)
- Only runs after **10s idle** (debug) / 30s (production)

## Testing Strategy

### 1. Start with debug logging
```bash
./crush --debug
```

### 2. Generate test content
- Paste a long error log (user_text)
- Ask for a verbose explanation (assistant)
- View a large file (tool_result)
- Use tools with long inputs (tool_call)

### 3. Wait for tidy to trigger
Wait 10+ seconds idle after conversation

### 4. Check logs
```bash
grep -i tidy .crush/logs/crush.log | tail -30
```

### 5. Expected log sequence
1. `"Tidy starting"` - Timer fired after idle
2. `"Tidy compression"` - Shows each ID, content preview
3. `"Tidy completed"` - Shows count of compressions stored
4. `"Tidy applied compressions"` - On next API call, compressions used

### 6. Verify compression worked
On the next API request, check the HTTP request body in logs - compressed content should have wrapper tags like `<summary>`, `<file_map>`, or `<compressed>`.

## Manual Tidy Command

Added `/tidy` command (available via `/` menu → "Run Tidy") to trigger tidy immediately without waiting for idle timeout.

### Implementation
- `TidyManager.RunNow()` - Bypasses idle timer, runs compression immediately
- `SessionAgent.RunTidy()` / `Coordinator.RunTidy()` - Exposed to UI layer
- `ActionRunTidy` - UI action wired to commands dialog

### Files Changed
- `internal/agent/tidy.go` - Added `RunNow()` method
- `internal/agent/agent.go` - Added `RunTidy()` to interface + implementation
- `internal/agent/coordinator.go` - Added `RunTidy()` to interface + implementation
- `internal/ui/dialog/actions.go` - Added `ActionRunTidy` type
- `internal/ui/dialog/commands.go` - Added "Run Tidy" to system commands
- `internal/ui/model/ui.go` - Handle `ActionRunTidy` action

## Debug Logging Added

| Log Message | Location | When |
|------------|----------|------|
| `"Tidy touch"` | `Touch()` | After each turn completes |
| `"Tidy starting"` | `doTidy()` | When idle timer fires |
| `"Tidy compression"` | `doTidy()` | For each item compressed |
| `"Tidy completed"` | `doTidy()` | After all compressions stored |
| `"Tidy applied compressions"` | `ApplyTidyCompressions()` | When compressions used in API call |
