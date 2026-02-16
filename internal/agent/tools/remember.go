package tools

import (
	"context"
	_ "embed"
	"fmt"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/memory"
)

//go:embed remember.md
var rememberDescription []byte

// RememberParams defines the parameters for the remember tool.
type RememberParams struct {
	Category string `json:"category" description:"Category of memory: 'preference' (user likes/dislikes), 'learning' (something learned about the codebase), 'decision' (why a choice was made), 'fact' (important information to retain)"`
	Content  string `json:"content" description:"The memory to store. Be specific and actionable. Good: 'User prefers tabs over spaces for Go code'. Bad: 'User has opinions about formatting'."`
}

const RememberToolName = "remember"

// NewRememberTool creates a tool that allows the agent to store persistent memories.
func NewRememberTool(store *memory.Store) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		RememberToolName,
		string(rememberDescription),
		func(ctx context.Context, params RememberParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Content == "" {
				return fantasy.NewTextErrorResponse("content is required"), nil
			}
			if params.Category == "" {
				params.Category = "learning"
			}

			// Validate category.
			validCategories := map[string]bool{
				"preference": true,
				"learning":   true,
				"decision":   true,
				"fact":       true,
			}
			if !validCategories[params.Category] {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("invalid category %q. Must be one of: preference, learning, decision, fact", params.Category),
				), nil
			}

			sessionID := GetSessionFromContext(ctx)

			entry := memory.Entry{
				Category: params.Category,
				Content:  params.Content,
				Source:   sessionID,
			}

			if err := store.Save(ctx, entry); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to save memory: %w", err)
			}

			return fantasy.NewTextResponse(
				fmt.Sprintf("Remembered [%s]: %s", params.Category, params.Content),
			), nil
		},
	)
}
