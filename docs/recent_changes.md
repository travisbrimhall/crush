# VS Code Context → Attachment Chip Refactor

## Summary

VS Code selections now render as compact attachment chips instead of large box-drawn code previews. This fixes the input area space issue where context blocks would consume most of the editor height, leaving no room to type.

## Changes

### `internal/message/attachment.go`
- Added `StartLine` and `EndLine` fields to `Attachment` struct for line range selections

### `internal/ui/attachments/attachments.go`
- Added `formatLabel()` method to show `filename:start-end` for selections with line ranges
- Updated `Render()` to use the new label format

### `internal/ui/model/ui.go`
- **Removed** `pendingContexts` field entirely
- **Modified** `handleExternalContext()` to convert VS Code selections to attachments immediately on arrival
- **Updated** `selectionContextToAttachment()` to include `StartLine`/`EndLine` fields
- **Removed** ~140 lines of now-unused code:
  - `renderPendingContexts()`
  - `renderSelectionContextBody()`
  - `pendingContextsHeight()`
  - `contextsToAttachments()`
- Simplified `renderEditorView()` and `updateSize()` calculations

## Result

- VS Code selections render as chips: `[󰨞 prompt_adapter.go:15-20]`
- Chips share the attachment list with drag-drop files and images
- Existing delete mode works (press key, then digit to delete)
- Attachments clear automatically on send
- Full textarea height available for typing
