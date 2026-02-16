package memory

import (
	"context"

	"github.com/charmbracelet/crush/internal/agent/prompt"
)

// PromptAdapter wraps a Store to implement prompt.MemoryReader.
type PromptAdapter struct {
	store *Store
}

// NewPromptAdapter creates a new adapter for prompt building.
func NewPromptAdapter(store *Store) *PromptAdapter {
	if store == nil {
		return nil
	}
	return &PromptAdapter{store: store}
}

// Recent implements prompt.MemoryReader.
func (a *PromptAdapter) Recent(ctx context.Context, limit int) ([]prompt.MemoryEntry, error) {
	entries, err := a.store.Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]prompt.MemoryEntry, len(entries))
	for i, e := range entries {
		result[i] = prompt.MemoryEntry{
			Category: e.Category,
			Content:  e.Content,
		}
	}
	return result, nil
}
