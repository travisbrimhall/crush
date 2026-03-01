# VS Code Context Integration: Tech Design

## Current State

### What Works
- VS Code extension sends `selection_context` events via `POST /context`
- Extension includes file path, selection range, and selection text in payload
- TUI receives and displays pending contexts above the editor
- Context blocks show source (VS Code), age, and file path

### Issues

1. **Context not cleared after send**: `m.pendingContexts` is not cleared when
   the user submits a message. Attachments are cleared via `m.attachments.Reset()`
   but contexts persist.

2. **Context not included in prompt**: The pending contexts are displayed but
   never actually sent to the LLM. The `sendMessage()` call only receives
   `value` and `attachments`, not contexts.

3. **No visual parity with attachments**: Attachments render as horizontal chips
   with file type icons. Context blocks use box-drawing characters which is
   inconsistent UX.

---

## Proposed Changes

### 1. Clear Contexts After Send

**File**: `internal/ui/model/ui.go` (around line 1617)

```go
// Current:
attachments := m.attachments.List()
m.attachments.Reset()

// Proposed:
attachments := m.attachments.List()
m.attachments.Reset()
pendingContexts := m.pendingContexts
m.pendingContexts = nil  // Clear after capturing
```

### 2. Include Contexts in Message

**Option A: Convert to Attachments** (Recommended)

Convert `selection_context` entries to `message.Attachment` before sending.
This reuses existing attachment infrastructure.

```go
// In sendMessage preparation:
for _, ctx := range pendingContexts {
    if ctx.EventType == "selection_context" {
        // Extract selection text from payload
        var payload struct {
            Selection struct {
                Text string `json:"text"`
            } `json:"selection"`
        }
        json.Unmarshal(ctx.Payload, &payload)
        
        att := message.Attachment{
            Type:     message.AttachmentTypeText,
            FilePath: ctx.FilePath,
            Content:  payload.Selection.Text,
            // Add metadata for rendering
        }
        attachments = append(attachments, att)
    }
}
```

**Option B: New Message Field**

Add a `contexts` field to message handling. More work, but cleaner separation.

**Recommendation**: Option A. Leverage existing attachment pipeline.

### 3. Unify Visual Rendering

Render external contexts the same way as file attachments - as horizontal chips
with source-specific icons.

**Current attachment rendering** (`internal/ui/attachments/attachments.go`):
- Uses `Renderer.Render()` to produce horizontal chip layout
- Icons based on file extension
- Shows filename, delete affordance

**Proposed context rendering**:
- VS Code: Use editor icon (📝 or similar)
- Format: `[📝 file.go:15-42]` or `[VS Code: selection in file.go]`
- Same chip styling as attachments
- Same delete interaction (if needed)

**Implementation**:

```go
// internal/ui/attachments/context_chip.go (new file)

// RenderContextChip renders an external context as a chip.
func RenderContextChip(ctx *ctxserver.Entry, styles Styles) string {
    icon := contextIcon(ctx.Source)  // 📝 for vscode, 🐳 for docker, etc.
    
    label := ctx.FilePath
    if ctx.EventType == "selection_context" {
        // Extract line range from payload
        label = fmt.Sprintf("%s:%d-%d", ctx.FilePath, startLine, endLine)
    }
    
    return renderChip(icon, label, styles)
}
```

Then in `renderEditorView()`, render contexts as chips alongside attachments:

```go
var chips []string
for _, ctx := range m.pendingContexts {
    chips = append(chips, attachments.RenderContextChip(ctx, s))
}
for _, att := range m.attachments.List() {
    chips = append(chips, attachments.RenderAttachmentChip(att, s))
}
// Render chips horizontally above textarea
```

---

## Implementation Plan

### Phase 1: Fix the Bug (Clear + Include)

1. Capture and clear `pendingContexts` in submit handler
2. Convert selection contexts to text attachments
3. Test: send selection, verify it appears in prompt, verify UI clears

### Phase 2: Visual Unification

1. Create `RenderContextChip()` function
2. Modify `renderEditorView()` to render contexts as chips
3. Remove box-drawing context blocks
4. Add delete interaction for contexts (optional, matches attachments)

### Phase 3: Enhanced Context Display (Optional)

1. Show line range in chip: `[📝 prompt_adapter.go:15-42]`
2. Expandable preview on hover/click
3. Count badge for duplicates: `[📝 file.go ×3]`

---

## Schema Alignment

Current `selection_context` payload (from extension.ts):

```json
{
  "schemaVersion": "1.0",
  "event": "selection_context",
  "source": "vscode",
  "timestamp": "...",
  "workspace": { "id": "...", "root": "...", "name": "..." },
  "file": { "path": "...", "languageId": "...", "version": 42 },
  "selection": {
    "range": { "start": { "line": 10, "character": 0 }, "end": { "line": 20, "character": 15 } },
    "text": "selected code here..."
  }
}
```

This is already well-structured. The `selection.text` field contains the actual
content, so we don't need MCP to pull it (unlike diagnostics which are
metadata-only).

**Note**: The design doc (`external_comms.md`) says payloads should be
"metadata-only" with Crush pulling content via MCP. However, for selections
the user explicitly chose the code - including the text directly is simpler
and matches user intent. Consider this an intentional deviation for UX.

---

## Files to Modify

| File | Change |
|------|--------|
| `internal/ui/model/ui.go` | Clear contexts on send, convert to attachments |
| `internal/ui/attachments/` | Add context chip rendering |
| `internal/context/types.go` | May need helper to extract selection text |

---

## Testing

1. **Manual**: Select code in VS Code → Send to Crush → Verify chip appears →
   Submit message → Verify context included in prompt → Verify chip cleared

2. **Unit tests**: Add tests for context-to-attachment conversion

3. **Edge cases**:
   - Empty selection (cursor position only)
   - Very large selection (should truncate?)
   - Multiple contexts from different sources
   - Context from wrong workspace (should already be rejected by server)
