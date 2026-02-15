# Crush Customization Notes

Notes and learnings from customizing the Crush TUI.

## UI Architecture

### Key Directories

| Directory | Purpose |
|-----------|---------|
| `internal/ui/model/` | Main UI model, state management, message routing |
| `internal/ui/chat/` | Chat message rendering (user, assistant, tools) |
| `internal/ui/dialog/` | Modal dialogs (sessions, models, commands, context) |
| `internal/ui/styles/` | All style definitions (colors, borders, padding) |
| `internal/ui/list/` | Generic list component with lazy rendering |
| `internal/ui/logo/` | Logo rendering |

### Style System

All styles live in `internal/ui/styles/styles.go`. Key patterns:

```go
// Semantic colors
s.Primary       // Pink/magenta - used for user messages
s.Secondary     // Purple - used for branding, agent
s.Tertiary      // Third accent
s.BgSubtle      // Subtle background for highlighting
s.FgMuted       // Dimmed text
s.FgSubtle      // Very faint text

// Applying styles
style := lipgloss.NewStyle().
    PaddingLeft(2).
    MarginBottom(1).
    BorderLeft(true).
    BorderForeground(color).
    Background(bgColor)
```

### Message Rendering

Chat messages are rendered in `internal/ui/chat/`:

- `user.go` - User message rendering
- `assistant.go` - Assistant response rendering  
- `tools.go` - Tool call/result rendering
- `messages.go` - Common message utilities, `AssistantInfoItem`

Message styles are defined in `styles.go`:
- `UserBlurred` / `UserFocused`
- `AssistantBlurred` / `AssistantFocused`
- `ToolCallBlurred` / `ToolCallFocused`

### Dialog Pattern

Dialogs implement the `Dialog` interface in `dialog/dialog.go`:

```go
type Dialog interface {
    ID() string
    HandleMsg(msg tea.Msg) Action
    Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor
    Cursor() *tea.Cursor
    ShortHelp() []key.Binding
    FullHelp() [][]key.Binding
}
```

To add a new dialog:
1. Create `dialog/myfeature.go` with the dialog struct
2. Create `dialog/myfeature_item.go` if it has list items
3. Add action types to `dialog/actions.go`
4. Add command to `dialog/commands.go` 
5. Wire up in `model/ui.go` (`openDialog` switch, action handling)

### List Component

The list in `internal/ui/list/` supports:
- `SetGap(n)` - spacing between items
- `SetSelected(idx)` - select item
- `SelectNext()` / `SelectPrev()` - navigation
- `ScrollToSelected()` - ensure selected visible
- `RegisterRenderCallback()` - modify items during render

**Important**: Register `FocusedRenderCallback` to make focus work:
```go
c.list = list.NewList(items...)
c.list.RegisterRenderCallback(list.FocusedRenderCallback(c.list))
```

## Changes Made

### Header Customizations

**File**: `internal/ui/model/header.go`

- Removed "Charm™ CRUSH" branding text
- Extended diagonal slashes (`╱`) to fill header
- Made slashes work as context usage progress bar:
  - Filled portion (secondary color) = context used
  - Empty portion (primary color) = remaining
  - Warning color (yellow) when >80% full

**File**: `internal/ui/styles/styles.go`

Added new header styles:
- `Header.DiagonalsFilled` - for used context
- `Header.DiagonalsWarning` - for high usage warning

### Chat Message Styling

**File**: `internal/ui/styles/styles.go`

- Increased assistant indent: `PaddingLeft(2)` → `PaddingLeft(4)`
- Added spacing: `MarginBottom(1)` to all message types
- Removed model info tags (the "◇ Claude Opus 4.5 via Anthropic" lines)

**File**: `internal/ui/model/ui.go`

- Removed `NewAssistantInfoItem` calls that rendered model tags

### Help Hints

**File**: `internal/ui/model/ui.go` (`ShortHelp` method)

Removed from bottom help bar:
- `ctrl+c quit`
- `shift+enter newline`  
- `esc cancel`

### Context Viewer Dialog

**New files**:
- `internal/ui/dialog/context.go` - Main dialog
- `internal/ui/dialog/context_item.go` - List item renderer

Features:
- Shows all messages in context with role icons
- Displays content preview and metadata (tool calls, results)
- Navigate with ↑↓/j/k
- Delete messages with `d` → confirm with `y`
- Access via command palette: "View/Edit Context"

Role display:
- `› Travis` - User messages (customized name)
- `🤖 Agent` - Assistant messages
- `🛠️ Tool` - Tool result messages
- `▪ System` - System messages

## Lessons Learned

### Emoji Width Issues

Emojis have variable display width which can break alignment. Solutions:
- Pad single-char icons with space to match emoji width
- Use `lipgloss.Width()` for accurate width calculation
- Test with actual terminal rendering

### Style Inheritance

Styles chain via method calls. Each call returns a new style:
```go
// This works - chained
style := base.PaddingLeft(1).BorderLeft(true).MarginBottom(1)

// Background may not extend full width without explicit Width()
style.Width(width).Render(content)
```

### List Focus Handling

Lists need the `FocusedRenderCallback` registered to properly track and render focused state. Without it, items won't show focus styling even when selected.

### Content Width Capping

Chat messages cap content at `maxTextWidth` (120 chars) for readability. If applying backgrounds, be aware the background won't extend beyond content width unless you explicitly set `Width()` on the style.

### Dialog Wiring

Adding a new dialog requires changes in multiple places:
1. Dialog implementation
2. Action types
3. Command palette entry
4. `openDialog()` switch in ui.go
5. Action handling in ui.go's main switch

### Testing UI Changes

Quick iteration:
```bash
go run .        # Run directly
go build . && ./crush  # Build then run
```

The UI updates live - just restart to see changes.
