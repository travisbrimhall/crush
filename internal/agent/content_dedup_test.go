package agent

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentDeduplicator_DedupeMessages(t *testing.T) {
	t.Parallel()

	makeToolMsg := func(content string) fantasy.Message {
		return fantasy.Message{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "test-id",
					Output:     fantasy.ToolResultOutputContentText{Text: content},
				},
			},
		}
	}

	getToolText := func(msg fantasy.Message) string {
		part := msg.Content[0]
		toolResult, _ := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
		text, _ := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](toolResult.Output)
		return text.Text
	}

	t.Run("keeps last occurrence, dedupes earlier ones", func(t *testing.T) {
		t.Parallel()

		fileContent := "<file>\n     1|package main\n     2|\n     3|func main() {}\n</file>"

		messages := []fantasy.Message{
			fantasy.NewUserMessage("read foo.go"),
			makeToolMsg(fileContent),
			fantasy.NewUserMessage("read foo.go again"),
			makeToolMsg(fileContent), // Same content.
		}

		d := newContentDeduplicator()
		count := d.DedupeMessages(messages)

		assert.Equal(t, 1, count, "should dedupe one occurrence")
		assert.Contains(t, getToolText(messages[1]), "unchanged", "first should be deduped")
		assert.Equal(t, fileContent, getToolText(messages[3]), "last should be unchanged")
	})

	t.Run("does not dedupe different contents", func(t *testing.T) {
		t.Parallel()

		file1 := "<file>\n     1|package main\n</file>"
		file2 := "<file>\n     1|package other\n</file>"

		messages := []fantasy.Message{
			makeToolMsg(file1),
			makeToolMsg(file2),
		}

		d := newContentDeduplicator()
		count := d.DedupeMessages(messages)

		assert.Equal(t, 0, count, "should not dedupe different files")
		assert.Equal(t, file1, getToolText(messages[0]))
		assert.Equal(t, file2, getToolText(messages[1]))
	})

	t.Run("ignores non-view outputs", func(t *testing.T) {
		t.Parallel()

		bashOutput := "command output without file tags"

		messages := []fantasy.Message{
			makeToolMsg(bashOutput),
			makeToolMsg(bashOutput), // Same but not view format.
		}

		d := newContentDeduplicator()
		count := d.DedupeMessages(messages)

		assert.Equal(t, 0, count, "should not dedupe non-view outputs")
		assert.Equal(t, bashOutput, getToolText(messages[0]))
		assert.Equal(t, bashOutput, getToolText(messages[1]))
	})

	t.Run("ignores non-tool messages", func(t *testing.T) {
		t.Parallel()

		messages := []fantasy.Message{
			fantasy.NewUserMessage("hello"),
			fantasy.NewSystemMessage("system"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "response"}}},
		}

		d := newContentDeduplicator()
		count := d.DedupeMessages(messages)

		assert.Equal(t, 0, count, "should not process non-tool messages")
	})

	t.Run("handles multiple files correctly", func(t *testing.T) {
		t.Parallel()

		fileA := "<file>\n     1|// file A\n</file>"
		fileB := "<file>\n     1|// file B\n</file>"

		messages := []fantasy.Message{
			makeToolMsg(fileA), // First A - will be deduped.
			makeToolMsg(fileB), // First B - will be deduped.
			makeToolMsg(fileA), // Second A - will be deduped.
			makeToolMsg(fileB), // Second B - kept (last B).
			makeToolMsg(fileA), // Third A - kept (last A).
		}

		d := newContentDeduplicator()
		count := d.DedupeMessages(messages)

		assert.Equal(t, 3, count, "should dedupe 3 occurrences")
		assert.Contains(t, getToolText(messages[0]), "unchanged", "first A deduped")
		assert.Contains(t, getToolText(messages[1]), "unchanged", "first B deduped")
		assert.Contains(t, getToolText(messages[2]), "unchanged", "second A deduped")
		assert.Equal(t, fileB, getToolText(messages[3]), "last B unchanged")
		assert.Equal(t, fileA, getToolText(messages[4]), "last A unchanged")
	})

	t.Run("preserves tool call ID", func(t *testing.T) {
		t.Parallel()

		fileContent := "<file>\n     1|content\n</file>"

		messages := []fantasy.Message{
			{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: "first-call",
						Output:     fantasy.ToolResultOutputContentText{Text: fileContent},
					},
				},
			},
			{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: "second-call",
						Output:     fantasy.ToolResultOutputContentText{Text: fileContent},
					},
				},
			},
		}

		d := newContentDeduplicator()
		d.DedupeMessages(messages)

		// Check that tool call IDs are preserved.
		part1, _ := fantasy.AsMessagePart[fantasy.ToolResultPart](messages[0].Content[0])
		part2, _ := fantasy.AsMessagePart[fantasy.ToolResultPart](messages[1].Content[0])

		assert.Equal(t, "first-call", part1.ToolCallID)
		assert.Equal(t, "second-call", part2.ToolCallID)
	})

	t.Run("handles empty messages", func(t *testing.T) {
		t.Parallel()

		d := newContentDeduplicator()
		count := d.DedupeMessages(nil)
		assert.Equal(t, 0, count)

		count = d.DedupeMessages([]fantasy.Message{})
		assert.Equal(t, 0, count)
	})
}

func TestParseViewOutput(t *testing.T) {
	t.Parallel()

	t.Run("parses standard view output", func(t *testing.T) {
		t.Parallel()

		input := `<file>
     1|package main
     2|
     3|func main() {}
</file>`

		_, content, ok := parseViewOutput(input)
		require.True(t, ok)
		assert.Contains(t, content, "package main")
		assert.Contains(t, content, "func main()")
	})

	t.Run("handles file with diagnostics", func(t *testing.T) {
		t.Parallel()

		input := `<file>
     1|package main
</file>
LSP Diagnostics: 0 errors, 0 warnings`

		_, content, ok := parseViewOutput(input)
		require.True(t, ok)
		assert.Contains(t, content, "package main")
	})

	t.Run("rejects non-file output", func(t *testing.T) {
		t.Parallel()

		_, _, ok := parseViewOutput("just some text")
		assert.False(t, ok)
	})

	t.Run("rejects empty file tags", func(t *testing.T) {
		t.Parallel()

		_, _, ok := parseViewOutput("<file>\n</file>")
		assert.False(t, ok)
	})
}

func TestHashContent(t *testing.T) {
	t.Parallel()

	t.Run("same content same hash", func(t *testing.T) {
		t.Parallel()

		content := "package main\nfunc main() {}"
		h1 := hashContent(content)
		h2 := hashContent(content)
		assert.Equal(t, h1, h2)
	})

	t.Run("different content different hash", func(t *testing.T) {
		t.Parallel()

		h1 := hashContent("content A")
		h2 := hashContent("content B")
		assert.NotEqual(t, h1, h2)
	})

	t.Run("hash is short", func(t *testing.T) {
		t.Parallel()

		h := hashContent(strings.Repeat("x", 10000))
		assert.Len(t, h, 16, "hash should be 16 hex chars (8 bytes)")
	})
}

func TestDedupeToolOutputs(t *testing.T) {
	t.Parallel()

	// Convenience function test.
	fileContent := "<file>\n     1|test\n</file>"
	messages := []fantasy.Message{
		{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "1",
					Output:     fantasy.ToolResultOutputContentText{Text: fileContent},
				},
			},
		},
		{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "2",
					Output:     fantasy.ToolResultOutputContentText{Text: fileContent},
				},
			},
		},
	}

	count := DedupeToolOutputs(messages)
	assert.Equal(t, 1, count)
}
