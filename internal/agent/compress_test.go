package agent

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
)

func TestCompressText(t *testing.T) {
	t.Parallel()

	t.Run("short text unchanged", func(t *testing.T) {
		text := "line1\nline2\nline3"
		assert.Equal(t, text, compressText(text))
	})

	t.Run("long text compressed", func(t *testing.T) {
		lines := make([]string, 100)
		for i := range lines {
			lines[i] = "line content here"
		}
		text := strings.Join(lines, "\n")

		result := compressText(text)
		assert.Contains(t, result, "line content here")
		assert.Contains(t, result, "lines omitted")
		assert.Less(t, len(result), len(text))
	})

	t.Run("long hashes shortened", func(t *testing.T) {
		text := "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
		result := compressText(text)
		assert.Contains(t, result, "9f86d081")
		assert.Contains(t, result, "b0f00a08")
		assert.Contains(t, result, "...")
		assert.Less(t, len(result), len(text))
	})

	t.Run("long URLs shortened", func(t *testing.T) {
		text := "https://s3.amazonaws.com/bucket/path/to/some/really/long/object/name/with/many/segments/file-abc123def456ghi789.tar.gz?token=verylongtokenvalue"
		result := compressText(text)
		assert.Contains(t, result, "https://s3.amazonaws.com")
		assert.Contains(t, result, "...")
		assert.Less(t, len(result), len(text))
	})
}

func TestCompressOldToolResults(t *testing.T) {
	t.Parallel()

	makeTool := func(text string) fantasy.Message {
		return fantasy.Message{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "test",
					Output:     fantasy.ToolResultOutputContentText{Text: text},
				},
			},
		}
	}

	makeUser := func(text string) fantasy.Message {
		return fantasy.NewUserMessage(text)
	}

	t.Run("recent messages not compressed", func(t *testing.T) {
		longText := strings.Repeat("x", 2000)
		history := []fantasy.Message{
			makeUser("hi"),
			makeTool(longText),
		}

		result := compressOldToolResults(history)
		toolResult := result[1].Content[0].(fantasy.ToolResultPart)
		textContent := toolResult.Output.(fantasy.ToolResultOutputContentText)
		assert.Equal(t, longText, textContent.Text)
	})

	t.Run("old large messages compressed", func(t *testing.T) {
		lines := make([]string, 100)
		for i := range lines {
			lines[i] = "some file content"
		}
		longText := strings.Join(lines, "\n")

		// Build history with old tool result and enough recent messages.
		history := []fantasy.Message{makeTool(longText)}
		for i := 0; i < 20; i++ {
			history = append(history, makeUser("msg"))
		}

		result := compressOldToolResults(history)
		toolResult := result[0].Content[0].(fantasy.ToolResultPart)
		textContent := toolResult.Output.(fantasy.ToolResultOutputContentText)
		assert.Contains(t, textContent.Text, "omitted")
		assert.Less(t, len(textContent.Text), len(longText))
	})
}
