package chat

import (
	"encoding/json"
	"fmt"
	"regexp"
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

// grepLineInfoRegex matches "Line X, Char Y:" or "Line X:" at the start of a line.
var grepLineInfoRegex = regexp.MustCompile(`^(\s*)(Line \d+(?:, Char \d+)?:)(.*)$`)

// toolOutputGrepContent renders grep output with styled file paths and line info.
func toolOutputGrepContent(sty *styles.Styles, content, pattern string, literalText bool, width int, expanded bool) string {
	content = stringext.NormalizeSpace(content)
	lines := strings.Split(content, "\n")

	// Compile the pattern for highlighting
	var matchRegex *regexp.Regexp
	if pattern != "" {
		if literalText {
			pattern = regexp.QuoteMeta(pattern)
		}
		// Case-insensitive matching for highlighting
		matchRegex, _ = regexp.Compile("(?i)(" + pattern + ")")
	}

	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines)
	}

	var out []string
	for i, ln := range lines {
		if i >= maxLines {
			break
		}

		styled := styleGrepLine(sty, ln, matchRegex, width)
		out = append(out, styled)
	}

	wasTruncated := len(lines) > responseContextHeight
	if !expanded && wasTruncated {
		out = append(out, sty.Tool.ContentTruncation.
			Width(width).
			Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-responseContextHeight)))
	}

	return strings.Join(out, "\n")
}

// styleGrepLine applies styling to a single grep output line.
func styleGrepLine(sty *styles.Styles, line string, matchRegex *regexp.Regexp, width int) string {
	// Empty lines - no background
	if strings.TrimSpace(line) == "" {
		return ""
	}

	line = " " + line

	// "Found X matches" or "No files found"
	if strings.HasPrefix(line, " Found ") || strings.HasPrefix(line, " No files") {
		if strings.HasPrefix(line, " No files") {
			line = " No files found ¯\\_(ツ)_/¯"
		}
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "…")
		}
		return sty.Tool.GrepMatchCount.Width(width).Render(line)
	}

	// File path lines (end with ":")
	if strings.HasSuffix(strings.TrimSpace(line), ":") && !strings.Contains(line, "Line ") {
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "…")
		}
		return sty.Tool.GrepFilePath.Width(width).Render(line)
	}

	// Match lines: "  Line X, Char Y: content"
	if matches := grepLineInfoRegex.FindStringSubmatch(line); matches != nil {
		indent := matches[1]
		lineInfo := matches[2]
		matchContent := matches[3]

		styledLineInfo := sty.Tool.GrepLineInfo.Render(indent + lineInfo)
		styledContent := highlightMatches(sty, matchContent, matchRegex)
		combined := styledLineInfo + styledContent

		if lipgloss.Width(combined) > width {
			combined = ansi.Truncate(combined, width, "…")
		}
		return combined
	}

	// Default: plain content line
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, "…")
	}
	return sty.Tool.ContentLine.Width(width).Render(line)
}

// highlightMatches highlights regex matches in the content.
func highlightMatches(sty *styles.Styles, content string, matchRegex *regexp.Regexp) string {
	if matchRegex == nil {
		return sty.Tool.GrepContent.Render(content)
	}

	// Find all match locations
	matches := matchRegex.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return sty.Tool.GrepContent.Render(content)
	}

	var result strings.Builder
	lastEnd := 0

	for _, match := range matches {
		start, end := match[0], match[1]
		// Add non-matching text before this match
		if start > lastEnd {
			result.WriteString(sty.Tool.GrepContent.Render(content[lastEnd:start]))
		}
		// Add the highlighted match
		result.WriteString(sty.Tool.GrepMatchHighlight.Render(content[start:end]))
		lastEnd = end
	}

	// Add any remaining text after the last match
	if lastEnd < len(content) {
		result.WriteString(sty.Tool.GrepContent.Render(content[lastEnd:]))
	}

	return result.String()
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
