package agent

import (
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/stretchr/testify/assert"
)

func TestApplyCacheMarkers(t *testing.T) {
	t.Parallel()

	cacheOpts := fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}

	hasCacheMarker := func(msg fantasy.Message) bool {
		return msg.ProviderOptions != nil && msg.ProviderOptions[anthropic.Name] != nil
	}

	t.Run("empty messages", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{}
		applyCacheMarkers(messages, false, cacheOpts)
		assert.Empty(t, messages)
	})

	t.Run("single system message", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("You are a helpful assistant"),
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.True(t, hasCacheMarker(messages[0]), "system message should be marked")
	})

	t.Run("multiple system messages marks only last", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("First system"),
			fantasy.NewSystemMessage("Second system"),
			fantasy.NewSystemMessage("Third system"),
			fantasy.NewUserMessage("Hello"),
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.False(t, hasCacheMarker(messages[0]), "first system should not be marked")
		assert.False(t, hasCacheMarker(messages[1]), "second system should not be marked")
		assert.True(t, hasCacheMarker(messages[2]), "last system should be marked")
		assert.True(t, hasCacheMarker(messages[3]), "user message in last 2 should be marked")
	})

	t.Run("marks last 2 messages", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("System"),
			fantasy.NewUserMessage("First user"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "First assistant"}}},
			fantasy.NewUserMessage("Second user"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Second assistant"}}},
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.True(t, hasCacheMarker(messages[0]), "system message should be marked")
		assert.False(t, hasCacheMarker(messages[1]), "first user should not be marked")
		assert.False(t, hasCacheMarker(messages[2]), "first assistant should not be marked")
		assert.True(t, hasCacheMarker(messages[3]), "second-to-last should be marked")
		assert.True(t, hasCacheMarker(messages[4]), "last should be marked")
	})

	t.Run("with summary marks first user message", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("System prompt"),
			fantasy.NewUserMessage("Summary of previous conversation"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Assistant response"}}},
			fantasy.NewUserMessage("New user message"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "New response"}}},
		}
		applyCacheMarkers(messages, true, cacheOpts)

		assert.True(t, hasCacheMarker(messages[0]), "system should be marked")
		assert.True(t, hasCacheMarker(messages[1]), "summary (first user) should be marked")
		assert.False(t, hasCacheMarker(messages[2]), "first assistant should not be marked")
		assert.True(t, hasCacheMarker(messages[3]), "second-to-last should be marked")
		assert.True(t, hasCacheMarker(messages[4]), "last should be marked")
	})

	t.Run("without summary does not mark first user", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("System prompt"),
			fantasy.NewUserMessage("First user message"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "First response"}}},
			fantasy.NewUserMessage("Second user message"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Second response"}}},
			fantasy.NewUserMessage("Third user message"),
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.True(t, hasCacheMarker(messages[0]), "system should be marked")
		assert.False(t, hasCacheMarker(messages[1]), "first user should not be marked (no summary)")
		assert.False(t, hasCacheMarker(messages[2]), "first assistant should not be marked")
		assert.False(t, hasCacheMarker(messages[3]), "second user should not be marked")
		assert.True(t, hasCacheMarker(messages[4]), "second-to-last should be marked")
		assert.True(t, hasCacheMarker(messages[5]), "last should be marked")
	})

	t.Run("template context and prompt prefix scenario", func(t *testing.T) {
		t.Parallel()
		// Simulates: promptPrefix, templateContext, then user message.
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("Prompt prefix system"),
			fantasy.NewSystemMessage("Template context system"),
			fantasy.NewUserMessage("User query"),
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.False(t, hasCacheMarker(messages[0]), "first system should not be marked")
		assert.True(t, hasCacheMarker(messages[1]), "last system (template) should be marked")
		assert.True(t, hasCacheMarker(messages[2]), "user query in last 2 should be marked")
	})

	t.Run("short conversation all marked", func(t *testing.T) {
		t.Parallel()
		// With only 2 messages, both are in "last 2".
		messages := []fantasy.Message{
			fantasy.NewSystemMessage("System"),
			fantasy.NewUserMessage("Hello"),
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.True(t, hasCacheMarker(messages[0]), "system marked (last system + in last 2)")
		assert.True(t, hasCacheMarker(messages[1]), "user marked (in last 2)")
	})

	t.Run("only user messages no system", func(t *testing.T) {
		t.Parallel()
		messages := []fantasy.Message{
			fantasy.NewUserMessage("First"),
			fantasy.NewUserMessage("Second"),
			fantasy.NewUserMessage("Third"),
		}
		applyCacheMarkers(messages, false, cacheOpts)

		assert.False(t, hasCacheMarker(messages[0]), "first should not be marked")
		assert.True(t, hasCacheMarker(messages[1]), "second-to-last should be marked")
		assert.True(t, hasCacheMarker(messages[2]), "last should be marked")
	})

	t.Run("summary with no system messages", func(t *testing.T) {
		t.Parallel()
		// Edge case: summary flag but no system messages.
		messages := []fantasy.Message{
			fantasy.NewUserMessage("Summary"),
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Response"}}},
			fantasy.NewUserMessage("Follow up"),
		}
		applyCacheMarkers(messages, true, cacheOpts)

		assert.True(t, hasCacheMarker(messages[0]), "summary should be marked")
		assert.True(t, hasCacheMarker(messages[1]), "second-to-last should be marked")
		assert.True(t, hasCacheMarker(messages[2]), "last should be marked")
	})
}
