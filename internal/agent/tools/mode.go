package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/modes"
)

//go:embed mode.md
var modeDescription []byte

// ModeParams defines the parameters for the mode tool.
type ModeParams struct {
	Action string `json:"action" description:"Action to perform: 'list', 'activate', 'deactivate', or 'status'"`
	Name   string `json:"name,omitempty" description:"Mode name (required for 'activate' action)"`
}

const ModeToolName = "mode"

// ModeCallback is called when a mode is activated or deactivated.
// The callback receives the session ID and the new mode context (empty string for deactivation).
type ModeCallback func(sessionID string, modeContext string)

// NewModeTool creates a tool for switching between context modes.
func NewModeTool(manager *modes.Manager, callback ModeCallback) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ModeToolName,
		string(modeDescription),
		func(ctx context.Context, params ModeParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)

			switch strings.ToLower(params.Action) {
			case "list":
				modeList := manager.List()
				if len(modeList) == 0 {
					return fantasy.NewTextResponse("No modes available. Create modes in ~/.crush/modes/"), nil
				}
				var sb strings.Builder
				sb.WriteString("Available modes:\n\n")
				for _, m := range modeList {
					sb.WriteString(fmt.Sprintf("- **%s**: %s\n", m.Name, m.Description))
					if len(m.MemoryTags) > 0 {
						sb.WriteString(fmt.Sprintf("  - Memory tags: %s\n", strings.Join(m.MemoryTags, ", ")))
					}
					if len(m.ContextFiles) > 0 {
						sb.WriteString(fmt.Sprintf("  - Context files: %d\n", len(m.ContextFiles)))
					}
				}
				return fantasy.NewTextResponse(sb.String()), nil

			case "activate":
				if params.Name == "" {
					return fantasy.NewTextErrorResponse("mode name is required for activation"), nil
				}
				modeContext, err := manager.Activate(ctx, sessionID, params.Name)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				if callback != nil {
					callback(sessionID, modeContext)
				}
				mode := manager.Active(sessionID)
				return fantasy.NewTextResponse(fmt.Sprintf("Activated **%s** mode. %s", mode.Name, mode.Description)), nil

			case "deactivate", "off":
				active := manager.Active(sessionID)
				if active == nil {
					return fantasy.NewTextResponse("No mode is currently active."), nil
				}
				name := active.Name
				manager.Deactivate(sessionID)
				if callback != nil {
					callback(sessionID, "")
				}
				return fantasy.NewTextResponse(fmt.Sprintf("Deactivated **%s** mode. Back to default context.", name)), nil

			case "status":
				active := manager.Active(sessionID)
				if active == nil {
					return fantasy.NewTextResponse("No mode is currently active."), nil
				}
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Current mode: **%s**\n", active.Name))
				sb.WriteString(fmt.Sprintf("Description: %s\n", active.Description))
				if len(active.MemoryTags) > 0 {
					sb.WriteString(fmt.Sprintf("Memory tags: %s\n", strings.Join(active.MemoryTags, ", ")))
				}
				return fantasy.NewTextResponse(sb.String()), nil

			default:
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("unknown action %q. Use: list, activate, deactivate, or status", params.Action),
				), nil
			}
		},
	)
}
