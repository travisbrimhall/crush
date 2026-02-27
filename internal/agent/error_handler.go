package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"charm.land/fantasy"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/stringext"
	"github.com/charmbracelet/x/exp/charmtone"
)

// handleStreamError handles errors from agent.Stream(), ensuring proper cleanup
// of assistant message state. It force-closes unfinished tool calls, creates
// missing tool results, and sets the appropriate finish reason.
//
// The parentCtx parameter should be a non-cancelled context for DB writes,
// since the streaming context may already be cancelled.
func (a *sessionAgent) handleStreamError(
	parentCtx context.Context,
	state *RunState,
	streamErr error,
) error {
	if state.CurrentAssistant == nil {
		return streamErr
	}

	isCancelErr := errors.Is(streamErr, context.Canceled)
	isPermissionErr := errors.Is(streamErr, permission.ErrorPermissionDenied)

	// Ensure we finish thinking on error to close the reasoning state.
	state.CurrentAssistant.FinishThinking()

	// Force-close unfinished tool calls and create missing tool results.
	if err := a.cleanupToolCalls(parentCtx, state, isCancelErr, isPermissionErr); err != nil {
		return err
	}

	// Set appropriate finish reason based on error type.
	a.setFinishReason(state, streamErr, isCancelErr, isPermissionErr)

	// Persist the final assistant message state.
	if err := a.messages.Update(parentCtx, *state.CurrentAssistant); err != nil {
		return err
	}

	return streamErr
}

// cleanupToolCalls force-closes unfinished tool calls and creates missing tool
// result messages.
func (a *sessionAgent) cleanupToolCalls(
	ctx context.Context,
	state *RunState,
	isCancelErr bool,
	isPermissionErr bool,
) error {
	toolCalls := state.CurrentAssistant.ToolCalls()

	// List existing messages to check for missing tool results.
	msgs, err := a.messages.List(ctx, state.CurrentAssistant.SessionID)
	if err != nil {
		return err
	}

	for _, tc := range toolCalls {
		// Force-close unfinished tool calls.
		if !tc.Finished {
			tc.Finished = true
			tc.Input = "{}"
			state.CurrentAssistant.AddToolCall(tc)
			if err := a.messages.Update(ctx, *state.CurrentAssistant); err != nil {
				return err
			}
		}

		// Check if tool result already exists.
		if hasToolResult(msgs, tc.ID) {
			continue
		}

		// Create missing tool result.
		content := "There was an error while executing the tool"
		if isCancelErr {
			content = "Tool execution canceled by user"
		} else if isPermissionErr {
			content = "User denied permission"
		}

		toolResult := message.ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Content:    content,
			IsError:    true,
		}
		_, err := a.messages.Create(ctx, state.CurrentAssistant.SessionID, message.CreateMessageParams{
			Role:  message.Tool,
			Parts: []message.ContentPart{toolResult},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// hasToolResult checks if a tool result message exists for the given tool call ID.
func hasToolResult(msgs []message.Message, toolCallID string) bool {
	for _, msg := range msgs {
		if msg.Role != message.Tool {
			continue
		}
		for _, tr := range msg.ToolResults() {
			if tr.ToolCallID == toolCallID {
				return true
			}
		}
	}
	return false
}

// setFinishReason sets the appropriate finish reason on the assistant message
// based on the error type.
func (a *sessionAgent) setFinishReason(
	state *RunState,
	err error,
	isCancelErr bool,
	isPermissionErr bool,
) {
	const defaultTitle = "Provider Error"
	linkStyle := lipgloss.NewStyle().Foreground(charmtone.Guac).Underline(true)

	switch {
	case isCancelErr:
		state.CurrentAssistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")

	case isPermissionErr:
		state.CurrentAssistant.AddFinish(message.FinishReasonPermissionDenied, "User denied permission", "")

	case errors.Is(err, hyper.ErrNoCredits):
		url := hyper.BaseURL()
		link := linkStyle.Hyperlink(url, "id=hyper").Render(url)
		state.CurrentAssistant.AddFinish(message.FinishReasonError, "No credits", "You're out of credits. Add more at "+link)

	default:
		a.setProviderFinishReason(state, err, linkStyle, defaultTitle)
	}
}

// setProviderFinishReason handles provider-specific error finish reasons.
func (a *sessionAgent) setProviderFinishReason(
	state *RunState,
	err error,
	linkStyle lipgloss.Style,
	defaultTitle string,
) {
	var fantasyErr *fantasy.Error
	var providerErr *fantasy.ProviderError

	switch {
	case errors.As(err, &providerErr):
		if providerErr.Message == "The requested model is not supported." {
			url := "https://github.com/settings/copilot/features"
			link := linkStyle.Hyperlink(url, "id=copilot").Render(url)
			state.CurrentAssistant.AddFinish(
				message.FinishReasonError,
				"Copilot model not enabled",
				fmt.Sprintf("%q is not enabled in Copilot. Go to the following page to enable it. Then, wait 5 minutes before trying again. %s", state.Model.CatwalkCfg.Name, link),
			)
		} else {
			state.CurrentAssistant.AddFinish(
				message.FinishReasonError,
				cmp.Or(stringext.Capitalize(providerErr.Title), defaultTitle),
				providerErr.Message,
			)
		}

	case errors.As(err, &fantasyErr):
		state.CurrentAssistant.AddFinish(
			message.FinishReasonError,
			cmp.Or(stringext.Capitalize(fantasyErr.Title), defaultTitle),
			fantasyErr.Message,
		)

	default:
		state.CurrentAssistant.AddFinish(message.FinishReasonError, defaultTitle, err.Error())
	}
}
