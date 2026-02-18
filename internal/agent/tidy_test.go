package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindTidyCandidates(t *testing.T) {
	t.Parallel()

	makeToolMsg := func(toolCallID, content string) fantasy.Message {
		return fantasy.Message{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: toolCallID,
					Output:     fantasy.ToolResultOutputContentText{Text: content},
				},
			},
		}
	}

	t.Run("finds bulky old messages", func(t *testing.T) {
		t.Parallel()

		bulkyContent := strings.Repeat("x", 600) // Over 500 char threshold.
		smallContent := "small"

		// Need at least 6 messages after the candidate for it to be "old enough".
		messages := []fantasy.Message{
			makeToolMsg("old-bulky", bulkyContent), // Index 0 - old and bulky.
			makeToolMsg("old-small", smallContent), // Index 1 - old but small.
			fantasy.NewUserMessage("msg 2"),
			fantasy.NewUserMessage("msg 3"),
			fantasy.NewUserMessage("msg 4"),
			fantasy.NewUserMessage("msg 5"),
			fantasy.NewUserMessage("msg 6"),
			makeToolMsg("recent-bulky", bulkyContent), // Index 7 - bulky but recent.
		}

		candidates := FindTidyCandidates(messages)

		require.Len(t, candidates, 1)
		assert.Equal(t, "old-bulky", candidates[0].ToolCallID)
		assert.Equal(t, 0, candidates[0].MsgIndex)
	})

	t.Run("ignores recent messages", func(t *testing.T) {
		t.Parallel()

		bulkyContent := strings.Repeat("x", 600)

		// Only 4 messages - none are "old enough" (need 6+ messages after).
		messages := []fantasy.Message{
			makeToolMsg("bulky-1", bulkyContent),
			fantasy.NewUserMessage("msg 1"),
			fantasy.NewUserMessage("msg 2"),
			makeToolMsg("bulky-2", bulkyContent),
		}

		candidates := FindTidyCandidates(messages)
		assert.Empty(t, candidates)
	})

	t.Run("ignores small content", func(t *testing.T) {
		t.Parallel()

		smallContent := "small content under 500 chars"

		messages := []fantasy.Message{
			makeToolMsg("small", smallContent),
			fantasy.NewUserMessage("msg 1"),
			fantasy.NewUserMessage("msg 2"),
			fantasy.NewUserMessage("msg 3"),
			fantasy.NewUserMessage("msg 4"),
			fantasy.NewUserMessage("msg 5"),
			fantasy.NewUserMessage("msg 6"),
		}

		candidates := FindTidyCandidates(messages)
		assert.Empty(t, candidates)
	})

	t.Run("handles empty messages", func(t *testing.T) {
		t.Parallel()

		candidates := FindTidyCandidates(nil)
		assert.Empty(t, candidates)

		candidates = FindTidyCandidates([]fantasy.Message{})
		assert.Empty(t, candidates)
	})
}

func TestBuildTidyPrompt(t *testing.T) {
	t.Parallel()

	t.Run("builds prompt with candidates", func(t *testing.T) {
		t.Parallel()

		candidates := []tidyCandidate{
			{ToolCallID: "call-1", Content: "content one"},
			{ToolCallID: "call-2", Content: "content two"},
		}

		prompt, err := BuildTidyPrompt(candidates)
		require.NoError(t, err)

		assert.Contains(t, prompt, "call-1")
		assert.Contains(t, prompt, "call-2")
		assert.Contains(t, prompt, "content one")
		assert.Contains(t, prompt, "content two")
	})

	t.Run("truncates long content", func(t *testing.T) {
		t.Parallel()

		longContent := strings.Repeat("x", 3000)
		candidates := []tidyCandidate{
			{ToolCallID: "call-1", Content: longContent},
		}

		prompt, err := BuildTidyPrompt(candidates)
		require.NoError(t, err)

		// Content should be truncated to ~2000 chars, prompt should not contain full 3000.
		assert.Contains(t, prompt, "truncated")
		assert.NotContains(t, prompt, strings.Repeat("x", 2500))
	})
}

func TestParseTidyResponse(t *testing.T) {
	t.Parallel()

	t.Run("parses valid JSON response", func(t *testing.T) {
		t.Parallel()

		response := `Here are the compressions:
[
  {"tool_call_id": "call-1", "summary": "Summary one"},
  {"tool_call_id": "call-2", "summary": "Summary two"}
]`

		compressions, err := ParseTidyResponse(response)
		require.NoError(t, err)
		require.Len(t, compressions, 2)

		assert.Equal(t, "call-1", compressions[0].ToolCallID)
		assert.Equal(t, "Summary one", compressions[0].Summary)
		assert.Equal(t, "call-2", compressions[1].ToolCallID)
		assert.Equal(t, "Summary two", compressions[1].Summary)
	})

	t.Run("parses empty array", func(t *testing.T) {
		t.Parallel()

		response := "Nothing to compress: []"

		compressions, err := ParseTidyResponse(response)
		require.NoError(t, err)
		assert.Len(t, compressions, 0)
	})

	t.Run("fails on no JSON array", func(t *testing.T) {
		t.Parallel()

		response := "No JSON here, just text."

		_, err := ParseTidyResponse(response)
		assert.Error(t, err)
	})

	t.Run("fails on invalid JSON", func(t *testing.T) {
		t.Parallel()

		response := "[invalid json}"

		_, err := ParseTidyResponse(response)
		assert.Error(t, err)
	})
}

func TestApplyTidyCompressions(t *testing.T) {
	t.Parallel()

	makeToolMsg := func(toolCallID, content string) fantasy.Message {
		return fantasy.Message{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: toolCallID,
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

	t.Run("applies compressions", func(t *testing.T) {
		t.Parallel()

		messages := []fantasy.Message{
			makeToolMsg("call-1", "original content 1"),
			makeToolMsg("call-2", "original content 2"),
			makeToolMsg("call-3", "original content 3"),
		}

		compressions := map[string]string{
			"call-1": "compressed 1",
			"call-3": "compressed 3",
		}

		count := ApplyTidyCompressions(messages, func(id string) (string, bool) {
			c, ok := compressions[id]
			return c, ok
		})

		assert.Equal(t, 2, count)
		assert.Equal(t, "compressed 1", getToolText(messages[0]))
		assert.Equal(t, "original content 2", getToolText(messages[1])) // Not compressed.
		assert.Equal(t, "compressed 3", getToolText(messages[2]))
	})

	t.Run("preserves tool call ID", func(t *testing.T) {
		t.Parallel()

		messages := []fantasy.Message{
			makeToolMsg("call-1", "original"),
		}

		ApplyTidyCompressions(messages, func(id string) (string, bool) {
			return "compressed", true
		})

		part := messages[0].Content[0]
		toolResult, _ := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
		assert.Equal(t, "call-1", toolResult.ToolCallID)
	})

	t.Run("handles no compressions", func(t *testing.T) {
		t.Parallel()

		messages := []fantasy.Message{
			makeToolMsg("call-1", "original"),
		}

		count := ApplyTidyCompressions(messages, func(id string) (string, bool) {
			return "", false
		})

		assert.Equal(t, 0, count)
		assert.Equal(t, "original", getToolText(messages[0]))
	})
}

func TestTidyManager(t *testing.T) {
	t.Parallel()

	t.Run("stores and retrieves compressions", func(t *testing.T) {
		t.Parallel()

		tm := NewTidyManager()

		// Simulate a completed tidy run by directly setting compressions.
		tm.mu.Lock()
		tm.sessions["session-1"] = &tidySession{
			compressions: map[string]string{
				"call-1": "compressed content",
			},
		}
		tm.mu.Unlock()

		content, found := tm.GetCompression("session-1", "call-1")
		assert.True(t, found)
		assert.Equal(t, "compressed content", content)

		_, found = tm.GetCompression("session-1", "call-2")
		assert.False(t, found)

		_, found = tm.GetCompression("session-2", "call-1")
		assert.False(t, found)
	})

	t.Run("stop removes session", func(t *testing.T) {
		t.Parallel()

		tm := NewTidyManager()

		// Touch to create session.
		tm.Touch("session-1", func(ctx context.Context) (map[string]string, error) {
			return nil, nil
		})

		tm.Stop("session-1")

		tm.mu.Lock()
		_, exists := tm.sessions["session-1"]
		tm.mu.Unlock()

		assert.False(t, exists)
	})
}
