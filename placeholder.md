# Summarize Function Investigation

## Problem
Auto-summarization at 80% context usage writes the summary successfully, but then Anthropic API throws a malformed payload error. Using Claude Max (OAuth-based access via `internal/oauth/claude/`).

## Key Files

| File | Purpose |
|------|---------|
| `internal/agent/agent.go:583-700` | `Summarize()` function - main logic |
| `internal/agent/agent.go:777-813` | `preparePrompt()` - converts messages to API format |
| `internal/message/content.go:463+` | `ToAIMessage()` - message serialization |
| `internal/cmd/summarize.go` | CLI command (calls `AgentCoordinator.Summarize()`) |

## Summarize Flow

1. Get session messages via `getSessionMessages()`
2. Convert to AI format via `preparePrompt()` → calls `ToAIMessage()` on each message
3. Create a new assistant message with `IsSummaryMessage: true`
4. Call `agent.Stream()` with:
   - System prompt: `summaryPrompt` (embedded)
   - User prompt: `buildSummaryPrompt(todos)`
   - Messages: full conversation history
5. Stream response, handle reasoning/thinking content
6. Save summary message, update session with `SummaryMessageID`

## Potential Issues

### 1. Message Role Conversion (line 831-832)
After summarization, when loading messages:
```go
if summaryMsgIndex != -1 {
    msgs = msgs[summaryMsgIndex:]
    msgs[0].Role = message.User  // <-- Summary (assistant) becomes user
}
```
This converts the summary message from `Assistant` to `User` role. If the original assistant message has:
- Reasoning content with signatures
- Tool calls
- Provider-specific metadata

...these might not serialize correctly as a User message.

### 2. Reasoning/Signature Handling (content.go:505-522)
Assistant messages include reasoning parts with provider-specific signatures:
```go
if reasoning.Signature != "" {
    reasoningPart.ProviderOptions[anthropic.Name] = &anthropic.ReasoningOptionMetadata{
        Signature: reasoning.Signature,
    }
}
```
When this assistant message becomes a user message, these parts may cause malformed payloads.

### 3. Empty Parts Skip Logic (agent.go:789-796)
Messages with no parts or empty assistant messages are skipped, but this logic may not account for all edge cases after role conversion.

## Next Steps

1. Add debug logging to see the exact payload being sent after summarization
2. Check what `ToAIMessage()` produces when an assistant message (with reasoning) is converted but its role is manually changed to User
3. Verify the fantasy library handles the converted message structure correctly
4. Consider whether summary messages should strip reasoning/signatures before role conversion

## Claude Max OAuth
- Uses `internal/oauth/claude/oauth.go`
- Authenticates via `https://claude.ai/oauth/authorize`
- Exchanges tokens via `https://console.anthropic.com/v1/oauth/token`
- Scope: `org:create_api_key user:profile user:inference`
