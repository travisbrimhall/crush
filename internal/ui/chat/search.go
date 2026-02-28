package chat

import (
	"encoding/json"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/stringext"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// -----------------------------------------------------------------------------
// Glob Tool
// -----------------------------------------------------------------------------

// GlobToolMessageItem is a message item that represents a glob tool call.
type GlobToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*GlobToolMessageItem)(nil)

// NewGlobToolMessageItem creates a new [GlobToolMessageItem].
func NewGlobToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &GlobToolRenderContext{}, canceled)
}

// GlobToolRenderContext renders glob tool messages.
type GlobToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (g *GlobToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if opts.IsPending() {
		return pendingTool(sty, "Glob", opts.Anim)
	}

	var params tools.GlobParams
	if err := json.Unmarshal([]byte(opts.ToolCall.Input), &params); err != nil {
		return toolErrorContent(sty, &message.ToolResult{Content: "Invalid parameters"}, cappedWidth)
	}

	toolParams := []string{params.Pattern}
	if params.Path != "" {
		toolParams = append(toolParams, "path", params.Path)
	}

	header := toolHeader(sty, opts.Status, "Glob", cappedWidth, opts.Compact, toolParams...)
	if opts.Compact {
		return header
	}

	if earlyState, ok := toolEarlyStateContent(sty, opts, cappedWidth); ok {
		return joinToolParts(header, earlyState)
	}

	if !opts.HasResult() || opts.Result.Content == "" {
		return header
	}

	bodyWidth := cappedWidth - toolBodyLeftPaddingTotal
	body := sty.Tool.Body.Render(toolOutputPlainContent(sty, opts.Result.Content, bodyWidth, opts.ExpandedContent))
	return joinToolParts(header, body)
}

// -----------------------------------------------------------------------------
// Grep Tool
// -----------------------------------------------------------------------------

// GrepToolMessageItem is a message item that represents a grep tool call.
type GrepToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*GrepToolMessageItem)(nil)

// NewGrepToolMessageItem creates a new [GrepToolMessageItem].
func NewGrepToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &GrepToolRenderContext{}, canceled)
}

// GrepToolRenderContext renders grep tool messages.
type GrepToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (g *GrepToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if opts.IsPending() {
		return pendingTool(sty, "Grep", opts.Anim)
	}

	var params tools.GrepParams
	if err := json.Unmarshal([]byte(opts.ToolCall.Input), &params); err != nil {
		return toolErrorContent(sty, &message.ToolResult{Content: "Invalid parameters"}, cappedWidth)
	}

	toolParams := []string{params.Pattern}
	if params.Path != "" {
		toolParams = append(toolParams, "path", params.Path)
	}
	if params.Include != "" {
		toolParams = append(toolParams, "include", params.Include)
	}
	if params.LiteralText {
		toolParams = append(toolParams, "literal", "true")
	}

	header := toolHeader(sty, opts.Status, "Grep", cappedWidth, opts.Compact, toolParams...)
	if opts.Compact {
		return header
	}

	if earlyState, ok := toolEarlyStateContent(sty, opts, cappedWidth); ok {
		return joinToolParts(header, earlyState)
	}

	if opts.HasEmptyResult() {
		return header
	}

	bodyWidth := cappedWidth - toolBodyLeftPaddingTotal
	body := sty.Tool.Body.Render(toolOutputGrepContent(sty, opts.Result.Content, params.Pattern, params.LiteralText, bodyWidth, opts.ExpandedContent))
	return joinToolParts(header, body)
}

// toolOutputGrepContent renders grep output with match count and file paths only.
func toolOutputGrepContent(sty *styles.Styles, content, pattern string, literalText bool, width int, expanded bool) string {
	content = stringext.NormalizeSpace(content)
	lines := strings.Split(content, "\n")

	var out []string

	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}

		// "Found X matches" or "No files found" - always show.
		if strings.HasPrefix(trimmed, "Found ") || strings.HasPrefix(trimmed, "No files") {
			line := " " + trimmed
			if strings.HasPrefix(trimmed, "No files") {
				line = " No files found ¯\\_(ツ)_/¯"
			}
			if lipgloss.Width(line) > width {
				line = ansi.Truncate(line, width, "…")
			}
			out = append(out, sty.Tool.GrepMatchCount.Width(width).Render(line))
			continue
		}

		// File path lines (end with ":") - show these.
		if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, "Line ") {
			line := " " + trimmed
			if lipgloss.Width(line) > width {
				line = ansi.Truncate(line, width, "…")
			}
			out = append(out, sty.Tool.GrepFilePath.Width(width).Render(line))
			continue
		}

		// Skip individual match lines (Line X, Char Y: content).
	}

	return strings.Join(out, "\n")
}

// -----------------------------------------------------------------------------
// LS Tool
// -----------------------------------------------------------------------------

// LSToolMessageItem is a message item that represents an ls tool call.
type LSToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*LSToolMessageItem)(nil)

// NewLSToolMessageItem creates a new [LSToolMessageItem].
func NewLSToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &LSToolRenderContext{}, canceled)
}

// LSToolRenderContext renders ls tool messages.
type LSToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (l *LSToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if opts.IsPending() {
		return pendingTool(sty, "List", opts.Anim)
	}

	var params tools.LSParams
	if err := json.Unmarshal([]byte(opts.ToolCall.Input), &params); err != nil {
		return toolErrorContent(sty, &message.ToolResult{Content: "Invalid parameters"}, cappedWidth)
	}

	path := params.Path
	if path == "" {
		path = "."
	}
	path = fsext.PrettyPath(path)

	header := toolHeader(sty, opts.Status, "List", cappedWidth, opts.Compact, path)
	if opts.Compact {
		return header
	}

	if earlyState, ok := toolEarlyStateContent(sty, opts, cappedWidth); ok {
		return joinToolParts(header, earlyState)
	}

	if opts.HasEmptyResult() {
		return header
	}

	bodyWidth := cappedWidth - toolBodyLeftPaddingTotal
	body := sty.Tool.Body.Render(toolOutputPlainContent(sty, opts.Result.Content, bodyWidth, opts.ExpandedContent))
	return joinToolParts(header, body)
}

// -----------------------------------------------------------------------------
// Sourcegraph Tool
// -----------------------------------------------------------------------------

// SourcegraphToolMessageItem is a message item that represents a sourcegraph tool call.
type SourcegraphToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*SourcegraphToolMessageItem)(nil)

// NewSourcegraphToolMessageItem creates a new [SourcegraphToolMessageItem].
func NewSourcegraphToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &SourcegraphToolRenderContext{}, canceled)
}

// SourcegraphToolRenderContext renders sourcegraph tool messages.
type SourcegraphToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (s *SourcegraphToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if opts.IsPending() {
		return pendingTool(sty, "Sourcegraph", opts.Anim)
	}

	var params tools.SourcegraphParams
	if err := json.Unmarshal([]byte(opts.ToolCall.Input), &params); err != nil {
		return toolErrorContent(sty, &message.ToolResult{Content: "Invalid parameters"}, cappedWidth)
	}

	toolParams := []string{params.Query}
	if params.Count != 0 {
		toolParams = append(toolParams, "count", formatNonZero(params.Count))
	}
	if params.ContextWindow != 0 {
		toolParams = append(toolParams, "context", formatNonZero(params.ContextWindow))
	}

	header := toolHeader(sty, opts.Status, "Sourcegraph", cappedWidth, opts.Compact, toolParams...)
	if opts.Compact {
		return header
	}

	if earlyState, ok := toolEarlyStateContent(sty, opts, cappedWidth); ok {
		return joinToolParts(header, earlyState)
	}

	if opts.HasEmptyResult() {
		return header
	}

	bodyWidth := cappedWidth - toolBodyLeftPaddingTotal
	body := sty.Tool.Body.Render(toolOutputPlainContent(sty, opts.Result.Content, bodyWidth, opts.ExpandedContent))
	return joinToolParts(header, body)
}
