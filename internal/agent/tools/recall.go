package tools

import (
	"context"
	_ "embed"
	"fmt"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/memory"
)

//go:embed recall.md
var recallDescription []byte

// RecallParams defines the parameters for the recall tool.
type RecallParams struct {
	Query    string `json:"query,omitempty" description:"Search query to find relevant memories. Leave empty to get recent memories."`
	Category string `json:"category,omitempty" description:"Filter by category: 'preference', 'learning', 'decision', 'fact'. Leave empty for all."`
	Limit    int    `json:"limit,omitempty" description:"Maximum number of memories to return. Default 10."`
	Hops     int    `json:"hops,omitempty" description:"For associative recall: how many links to follow from initial matches. Default 2."`
}

const RecallToolName = "recall"

// NewRecallTool creates a tool that allows the agent to retrieve stored memories.
// If an AssociativeMemoryStore is provided, uses graph traversal for queries.
func NewRecallTool(store memory.MemoryStore) fantasy.AgentTool {
	// Check if we have associative capabilities.
	assocStore, hasAssociative := store.(memory.AssociativeMemoryStore)

	return fantasy.NewAgentTool(
		RecallToolName,
		string(recallDescription),
		func(ctx context.Context, params RecallParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			limit := params.Limit
			if limit <= 0 {
				limit = 10
			}
			if limit > 50 {
				limit = 50
			}

			var entries []memory.Entry
			var err error

			if params.Query != "" {
				// Use associative retrieval if available.
				if hasAssociative {
					hops := params.Hops
					if hops <= 0 {
						hops = 2
					}
					entries, err = assocStore.Associate(ctx, params.Query, hops)
				} else {
					entries, err = store.Search(ctx, params.Query)
				}
			} else if params.Category != "" {
				entries, err = store.List(ctx, params.Category)
			} else {
				entries, err = store.Recent(ctx, limit)
			}

			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to recall memories: %w", err)
			}

			if len(entries) == 0 {
				return fantasy.NewTextResponse("No memories found."), nil
			}

			// Limit results.
			if len(entries) > limit {
				entries = entries[:limit]
			}

			output := memory.FormatForContext(entries)
			return fantasy.NewTextResponse(output), nil
		},
	)
}
