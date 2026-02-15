package dialog

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// ContextItem wraps a message.Message to implement list.Item.
type ContextItem struct {
	message.Message
	t       *styles.Styles
	mode    contextMode
	focused bool
}

var _ list.Item = &ContextItem{}
var _ list.Focusable = &ContextItem{}

// SetFocused implements list.Focusable.
func (c *ContextItem) SetFocused(focused bool) {
	c.focused = focused
}

// Render implements list.Item.
func (c *ContextItem) Render(width int) string {
	// Role indicator and styling.
	var roleIcon, roleLabel string
	var roleStyle, contentStyle lipgloss.Style

	// Use consistent icon width: single-char icons get a space padding.
	switch c.Role {
	case message.User:
		roleIcon = "› "
		roleLabel = "Travis"
		if c.focused {
			roleStyle = c.t.Base.Foreground(c.t.Secondary)
		} else {
			roleStyle = c.t.Base.Foreground(c.t.Primary)
		}
	case message.Assistant:
		roleIcon = "🤖"
		roleLabel = "Agent"
		roleStyle = c.t.Base.Foreground(c.t.Secondary)
	case message.Tool:
		roleIcon = "🛠️"
		roleLabel = "Tool"
		roleStyle = c.t.Base.Foreground(c.t.Secondary)
	case message.System:
		roleIcon = "▪ "
		roleLabel = "System"
		roleStyle = c.t.Muted
	default:
		roleIcon = "? "
		roleLabel = string(c.Role)
		roleStyle = c.t.Subtle
	}

	// Item styling based on focus and mode.
	itemStyle := c.t.Dialog.NormalItem
	if c.focused {
		itemStyle = c.t.Dialog.SelectedItem
		contentStyle = c.t.Base
	} else {
		contentStyle = c.t.Muted
	}

	if c.mode == contextModeDeleting && c.focused {
		itemStyle = c.t.Dialog.Sessions.DeletingItemFocused
	}

	// Get content preview.
	content := c.getContentPreview()

	// Count parts for metadata.
	toolCalls := 0
	toolResults := 0
	for _, part := range c.Parts {
		switch part.(type) {
		case message.ToolCall:
			toolCalls++
		case message.ToolResult:
			toolResults++
		}
	}

	// Build metadata string.
	var meta []string
	if toolCalls > 0 {
		meta = append(meta, fmt.Sprintf("%d tools", toolCalls))
	}
	if toolResults > 0 {
		meta = append(meta, fmt.Sprintf("%d results", toolResults))
	}
	if c.IsSummaryMessage {
		meta = append(meta, "summary")
	}

	metaStr := ""
	if len(meta) > 0 {
		metaStr = c.t.Subtle.Render(" [" + strings.Join(meta, ", ") + "]")
	}

	// Build the line.
	roleText := roleStyle.Render(roleIcon + " " + roleLabel)
	roleWidth := lipgloss.Width(roleText)
	metaWidth := lipgloss.Width(metaStr)

	availWidth := width - roleWidth - metaWidth - 4 // padding
	if availWidth < 10 {
		availWidth = 10
	}

	content = ansi.Truncate(content, availWidth, "…")
	content = contentStyle.Render(content)

	line := fmt.Sprintf("%s %s%s", roleText, content, metaStr)

	return itemStyle.Width(width).Render(line)
}

// getContentPreview returns a short preview of the message content.
func (c *ContextItem) getContentPreview() string {
	// Try to get text content first.
	text := c.Content()
	if text.Text != "" {
		// Clean up and truncate.
		preview := strings.ReplaceAll(text.Text, "\n", " ")
		preview = strings.TrimSpace(preview)
		return preview
	}

	// Check for tool calls - show all tool names.
	var toolNames []string
	for _, part := range c.Parts {
		if tc, ok := part.(message.ToolCall); ok {
			// Show tool name with a snippet of input if available.
			name := tc.Name
			if tc.Input != "" {
				// Try to show a useful snippet of the input.
				input := strings.ReplaceAll(tc.Input, "\n", " ")
				input = strings.TrimSpace(input)
				if len(input) > 40 {
					input = input[:40] + "…"
				}
				name = fmt.Sprintf("%s(%s)", name, input)
			}
			toolNames = append(toolNames, name)
		}
	}
	if len(toolNames) > 0 {
		return strings.Join(toolNames, ", ")
	}

	// Check for tool results - show tool name and result snippet.
	for _, part := range c.Parts {
		if tr, ok := part.(message.ToolResult); ok {
			preview := strings.ReplaceAll(tr.Content, "\n", " ")
			preview = strings.TrimSpace(preview)
			if len(preview) > 60 {
				preview = preview[:60] + "…"
			}
			if tr.Name != "" {
				return fmt.Sprintf("%s → %s", tr.Name, preview)
			}
			return preview
		}
	}

	// Check for images.
	images := c.ImageURLContent()
	if len(images) > 0 {
		return fmt.Sprintf("[%d image(s)]", len(images))
	}

	binaries := c.BinaryContent()
	if len(binaries) > 0 {
		return fmt.Sprintf("[%d file(s)]", len(binaries))
	}

	return "(empty)"
}
