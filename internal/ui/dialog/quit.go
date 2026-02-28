package dialog

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

// QuitID is the identifier for the quit dialog.
const QuitID = "quit"

// Quit represents a confirmation dialog for quitting the application.
type Quit struct {
	com   *common.Common
	input textinput.Model

	keyMap struct {
		Submit key.Binding
		Close  key.Binding
	}
}

var _ Dialog = (*Quit)(nil)

// NewQuit creates a new quit confirmation dialog.
func NewQuit(com *common.Common) *Quit {
	q := &Quit{
		com: com,
	}

	q.input = textinput.New()
	q.input.SetVirtualCursor(false)
	q.input.Placeholder = "type 'quit' to exit"
	q.input.SetStyles(com.Styles.TextInput)
	q.input.Focus()

	q.keyMap.Submit = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	)
	q.keyMap.Close = CloseKey

	return q
}

// ID implements [Model].
func (*Quit) ID() string {
	return QuitID
}

// HandleMsg implements [Model].
func (q *Quit) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, q.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, q.keyMap.Submit):
			if strings.EqualFold(strings.TrimSpace(q.input.Value()), "quit") {
				return ActionQuit{}
			}
			// Wrong input - could shake or show error, but for now just clear.
			q.input.SetValue("")
			return nil
		default:
			var cmd tea.Cmd
			q.input, cmd = q.input.Update(msg)
			return ActionCmd{cmd}
		}
	}

	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (q *Quit) Cursor() *tea.Cursor {
	cur := q.input.Cursor()
	if cur == nil {
		return nil
	}

	t := q.com.Styles
	borderStyle := t.BorderFocus
	inputStyle := t.Dialog.InputPrompt

	// X offset: border + padding + input margin.
	cur.X += borderStyle.GetBorderLeftSize() +
		borderStyle.GetPaddingLeft() +
		inputStyle.GetMarginLeft()

	// Y offset: border + padding + question line + blank line + input margin.
	const questionLines = 2 // "Type 'quit' to exit" + blank line
	cur.Y += borderStyle.GetBorderTopSize() +
		borderStyle.GetPaddingTop() +
		questionLines +
		inputStyle.GetMarginTop()

	return cur
}

// Draw implements [Dialog].
func (q *Quit) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := q.com.Styles
	const question = "Type 'quit' to exit"

	width := min(50, area.Dx())
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	q.input.SetWidth(innerWidth - t.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)

	inputView := t.Dialog.InputPrompt.Render(q.input.View())

	content := t.Base.Render(
		lipgloss.JoinVertical(
			lipgloss.Center,
			question,
			"",
			inputView,
		),
	)

	view := t.BorderFocus.Render(content)

	cur := q.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (q *Quit) ShortHelp() []key.Binding {
	return []key.Binding{
		q.keyMap.Submit,
		q.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (q *Quit) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{q.keyMap.Submit, q.keyMap.Close},
	}
}
