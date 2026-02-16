package memory

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	store, err := NewStore(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Test Save and Recent.
	err = store.Save(ctx, Entry{
		Category: "preference",
		Content:  "User prefers tabs over spaces",
		Source:   "session_123",
	})
	require.NoError(t, err)

	err = store.Save(ctx, Entry{
		Category: "learning",
		Content:  "Project uses gofumpt for formatting",
		Source:   "session_123",
	})
	require.NoError(t, err)

	recent, err := store.Recent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, recent, 2)

	// Test List by category.
	prefs, err := store.List(ctx, "preference")
	require.NoError(t, err)
	require.Len(t, prefs, 1)
	require.Equal(t, "User prefers tabs over spaces", prefs[0].Content)

	// Test Search.
	results, err := store.Search(ctx, "gofumpt")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "learning", results[0].Category)

	// Test Delete.
	err = store.Delete(ctx, recent[0].ID)
	require.NoError(t, err)

	remaining, err := store.Recent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
}

func TestPromptAdapter(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	store, err := NewStore(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Add some memories.
	err = store.Save(ctx, Entry{
		Category: "fact",
		Content:  "The main branch is called 'main'",
	})
	require.NoError(t, err)

	adapter := NewPromptAdapter(store)
	require.NotNil(t, adapter)

	entries, err := adapter.Recent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "fact", entries[0].Category)
	require.Equal(t, "The main branch is called 'main'", entries[0].Content)

	// Test nil store returns nil adapter.
	nilAdapter := NewPromptAdapter(nil)
	require.Nil(t, nilAdapter)
}

func TestFormatForContext(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Category: "preference", Content: "Prefers short variable names"},
		{Category: "preference", Content: "Likes verbose error messages"},
		{Category: "fact", Content: "Uses PostgreSQL in production"},
	}

	formatted := FormatForContext(entries)
	require.Contains(t, formatted, "<learned_memories>")
	require.Contains(t, formatted, "Prefers short variable names")
	require.Contains(t, formatted, "Uses PostgreSQL in production")
	require.Contains(t, formatted, "</learned_memories>")

	// Empty entries should return empty string.
	empty := FormatForContext(nil)
	require.Empty(t, empty)
}
