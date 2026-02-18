package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"

	"charm.land/fantasy"
)

// contentDeduplicator reduces token usage by replacing repeated file contents
// in conversation history with short references.
//
// When the same file is viewed multiple times, only the LAST occurrence keeps
// the full content. Earlier occurrences are replaced with a short reference.
// This ensures the model always has the most recent file state in context.
type contentDeduplicator struct {
	// contentLocations maps content hash -> all occurrence locations.
	contentLocations map[string][]contentLocation
}

type contentLocation struct {
	msgIdx  int // Message index in the slice.
	partIdx int // Part index within the message.
}

// newContentDeduplicator creates a new deduplicator instance.
func newContentDeduplicator() *contentDeduplicator {
	return &contentDeduplicator{
		contentLocations: make(map[string][]contentLocation),
	}
}

// DedupeMessages processes messages and replaces duplicate file contents with
// references. Only the LAST occurrence of each file keeps full content.
// Messages are modified in place.
//
// Returns the number of deduplications performed.
func (d *contentDeduplicator) DedupeMessages(messages []fantasy.Message) int {
	// First pass: find all file content occurrences.
	for i := range messages {
		msg := &messages[i]
		if msg.Role != fantasy.MessageRoleTool {
			continue
		}

		for j := range msg.Content {
			part := msg.Content[j]
			toolResult, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}

			text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](toolResult.Output)
			if !ok {
				continue
			}

			_, content, ok := parseViewOutput(text.Text)
			if !ok {
				continue
			}

			hash := hashContent(content)
			d.contentLocations[hash] = append(d.contentLocations[hash], contentLocation{
				msgIdx:  i,
				partIdx: j,
			})
		}
	}

	// Second pass: replace all but the last occurrence of each content.
	dedupeCount := 0
	for _, locations := range d.contentLocations {
		if len(locations) < 2 {
			continue
		}

		// Replace all except the last one.
		for _, loc := range locations[:len(locations)-1] {
			msg := &messages[loc.msgIdx]
			toolResult, _ := fantasy.AsMessagePart[fantasy.ToolResultPart](msg.Content[loc.partIdx])

			msg.Content[loc.partIdx] = fantasy.ToolResultPart{
				ToolCallID:      toolResult.ToolCallID,
				Output:          fantasy.ToolResultOutputContentText{Text: buildDedupeReference()},
				ProviderOptions: toolResult.ProviderOptions,
			}
			dedupeCount++
		}
	}

	return dedupeCount
}

// viewOutputPattern matches the View tool's output format.
// Expected format: <file>\n...content...\n</file>
var viewOutputPattern = regexp.MustCompile(`(?s)<file>\s*(.*?)\s*</file>`)

// parseViewOutput extracts the file path and content from a View tool response.
// Returns (filePath, content, ok).
func parseViewOutput(text string) (string, string, bool) {
	matches := viewOutputPattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return "", "", false
	}

	content := matches[1]
	if content == "" {
		return "", "", false
	}

	// Extract file path from the first line (format: "  123|...").
	// The file path isn't directly in the output, but we can use the content
	// hash as the identifier. For better UX, we'll try to find the path from
	// any "File:" prefix or similar patterns.
	//
	// For now, we use the content hash as the primary identifier, and include
	// a generic message. The file path context is typically clear from the
	// tool call parameters in the same message.
	filePath := "[same file]"

	return filePath, content, true
}

// hashContent returns a short hash of the content for comparison.
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:8]) // First 8 bytes = 16 hex chars.
}

// buildDedupeReference creates the replacement text for deduplicated content.
func buildDedupeReference() string {
	return "<file>\n[Content unchanged - see later in conversation for current state]\n</file>"
}

// DedupeToolOutputs is a convenience function that creates a deduplicator and
// processes the messages in one call.
func DedupeToolOutputs(messages []fantasy.Message) int {
	d := newContentDeduplicator()
	return d.DedupeMessages(messages)
}
