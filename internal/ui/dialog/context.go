package dialog

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	uv "github.com/charmbracelet/ultraviolet"
)

// ContextID is the identifier for the context viewer dialog.
const ContextID = "context"

type contextMode uint8

const (
	contextModeNormal contextMode = iota
	contextModeDeleting
)

// Context is a context viewer/editor dialog.
type Context struct {
	com       *common.Common
	help      help.Model
	list      *list.List
	sessionID string
	messages  []message.Message

	mode contextMode

	keyMap struct {
		Next          key.Binding
		Previous      key.Binding
		UpDown        key.Binding
		Delete        key.Binding
		ConfirmDelete key.Binding
		CancelDelete  key.Binding
		Close         key.Binding
	}
}

var _ Dialog = (*Context)(nil)

// NewContext creates a new Context dialog.
func NewContext(com *common.Common, sessionID string) (*Context, error) {
	c := &Context{
		com:       com,
		sessionID: sessionID,
		mode:      contextModeNormal,
	}

	messages, err := com.App.Messages.List(context.TODO(), sessionID)
	if err != nil {
		return nil, err
	}
	c.messages = messages

	helpModel := help.New()
	helpModel.Styles = com.Styles.DialogHelpStyles()
	c.help = helpModel

	c.list = list.NewList(contextItems(com, contextModeNormal, messages...)...)
	c.list.RegisterRenderCallback(list.FocusedRenderCallback(c.list))
	c.list.Focus()
	c.list.SetSelected(0)

	c.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n", "j"),
		key.WithHelp("↓", "next"),
	)
	c.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p", "k"),
		key.WithHelp("↑", "previous"),
	)
	c.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "navigate"),
	)
	c.keyMap.Delete = key.NewBinding(
		key.WithKeys("ctrl+x", "d"),
		key.WithHelp("d", "delete"),
	)
	c.keyMap.ConfirmDelete = key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "confirm"),
	)
	c.keyMap.CancelDelete = key.NewBinding(
		key.WithKeys("n", "esc"),
		key.WithHelp("n/esc", "cancel"),
	)
	c.keyMap.Close = CloseKey

	return c, nil
}

// ID implements Dialog.
func (c *Context) ID() string {
	return ContextID
}

// HandleMsg implements Dialog.
func (c *Context) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch c.mode {
		case contextModeDeleting:
			switch {
			case key.Matches(msg, c.keyMap.ConfirmDelete):
				action := c.confirmDeleteMessage()
				c.mode = contextModeNormal
				c.list.SetItems(contextItems(c.com, contextModeNormal, c.messages...)...)
				return action
			case key.Matches(msg, c.keyMap.CancelDelete):
				c.mode = contextModeNormal
				c.list.SetItems(contextItems(c.com, contextModeNormal, c.messages...)...)
				return nil
			}
		case contextModeNormal:
			switch {
			case key.Matches(msg, c.keyMap.Close):
				return ActionClose{}
			case key.Matches(msg, c.keyMap.Next):
				c.list.SelectNext()
				c.list.ScrollToSelected()
				return nil
			case key.Matches(msg, c.keyMap.Previous):
				c.list.SelectPrev()
				c.list.ScrollToSelected()
				return nil
			case key.Matches(msg, c.keyMap.Delete):
				if len(c.messages) > 0 {
					c.mode = contextModeDeleting
					c.list.SetItems(contextItems(c.com, contextModeDeleting, c.messages...)...)
				}
				return nil
			}
		}
	}
	return nil
}

func (c *Context) confirmDeleteMessage() Action {
	idx := c.list.Selected()
	if idx < 0 || idx >= len(c.messages) {
		return nil
	}

	msg := c.messages[idx]

	// Remove from local slice.
	c.messages = append(c.messages[:idx], c.messages[idx+1:]...)

	// Adjust selection if needed.
	if idx >= len(c.messages) && len(c.messages) > 0 {
		c.list.SetSelected(len(c.messages) - 1)
	}

	return ActionDeleteMessage{MessageID: msg.ID}
}

// Draw implements Dialog.
func (c *Context) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := c.com.Styles

	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	c.list.SetSize(innerWidth, height-heightOffset)
	c.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = fmt.Sprintf("Context (%d messages)", len(c.messages))

	switch c.mode {
	case contextModeDeleting:
		rc.TitleStyle = t.Dialog.Sessions.DeletingTitle
		rc.TitleGradientFromColor = t.Dialog.Sessions.DeletingTitleGradientFromColor
		rc.TitleGradientToColor = t.Dialog.Sessions.DeletingTitleGradientToColor
		rc.ViewStyle = t.Dialog.Sessions.DeletingView
		rc.AddPart(t.Dialog.Sessions.DeletingMessage.Render("Delete this message from context?"))
	}

	listView := t.Dialog.List.Height(c.list.Height()).Render(c.list.Render())
	rc.AddPart(listView)
	rc.Help = c.help.View(c)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, nil)
	return nil
}

// Cursor implements Dialog.
func (c *Context) Cursor() *tea.Cursor {
	return nil
}

// ShortHelp implements [help.KeyMap].
func (c *Context) ShortHelp() []key.Binding {
	switch c.mode {
	case contextModeDeleting:
		return []key.Binding{
			c.keyMap.ConfirmDelete,
			c.keyMap.CancelDelete,
		}
	default:
		return []key.Binding{
			c.keyMap.UpDown,
			c.keyMap.Delete,
			c.keyMap.Close,
		}
	}
}

// FullHelp implements [help.KeyMap].
func (c *Context) FullHelp() [][]key.Binding {
	return [][]key.Binding{c.ShortHelp()}
}

// contextItems creates list items from messages.
func contextItems(com *common.Common, mode contextMode, messages ...message.Message) []list.Item {
	items := make([]list.Item, len(messages))
	for i, msg := range messages {
		items[i] = &ContextItem{
			Message: msg,
			t:       com.Styles,
			mode:    mode,
		}
	}
	return items
}
