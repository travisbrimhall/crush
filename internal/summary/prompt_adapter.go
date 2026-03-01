package summary

import (
	"context"

	"github.com/charmbracelet/crush/internal/agent/prompt"
)

// PromptAdapter wraps a Store to implement prompt.SummaryReader.
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

// Recent implements prompt.SummaryReader.
func (a *PromptAdapter) Recent(ctx context.Context, limit int) ([]prompt.SummaryEntry, error) {
	summaries, err := a.store.Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]prompt.SummaryEntry, len(summaries))
	for i, s := range summaries {
		result[i] = prompt.SummaryEntry{
			SessionID: s.SessionID,
			Summary:   s.SummaryText,
		}
	}
	return result, nil
}
