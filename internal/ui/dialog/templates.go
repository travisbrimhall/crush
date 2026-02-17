package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/templates"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// TemplatesID is the identifier for the template selector dialog.
const TemplatesID = "templates"

// Templates is a template selector dialog.
type Templates struct {
	com       *common.Common
	help      help.Model
	list      *list.FilterableList
	templates []*templates.Template

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Skip     key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*Templates)(nil)

// NewTemplates creates a new Templates dialog.
func NewTemplates(com *common.Common, store *templates.Store) *Templates {
	t := &Templates{com: com}
	t.templates = store.List()

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	t.help = h

	t.list = list.NewFilterableList(templateItems(com.Styles, t.templates...)...)
	t.list.Focus()
	t.list.RegisterRenderCallback(list.FocusedRenderCallback(t.list.List))
	t.list.SetSelected(0)

	t.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "tab", "ctrl+y"),
		key.WithHelp("enter", "choose"),
	)
	t.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next"),
	)
	t.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous"),
	)
	t.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "navigate"),
	)
	t.keyMap.Skip = key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "skip"),
	)
	t.keyMap.Close = CloseKey

	return t
}

// ID implements Dialog.
func (t *Templates) ID() string {
	return TemplatesID
}

// HandleMsg implements Dialog.
func (t *Templates) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, t.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, t.keyMap.Skip):
			// Skip template selection - proceed with no template
			return ActionSelectTemplate{Template: nil}
		case key.Matches(msg, t.keyMap.Previous):
			t.list.Focus()
			if t.list.IsSelectedFirst() {
				t.list.SelectLast()
			} else {
				t.list.SelectPrev()
			}
			t.list.ScrollToSelected()
		case key.Matches(msg, t.keyMap.Next):
			t.list.Focus()
			if t.list.IsSelectedLast() {
				t.list.SelectFirst()
			} else {
				t.list.SelectNext()
			}
			t.list.ScrollToSelected()
		case key.Matches(msg, t.keyMap.Select):
			if item := t.list.SelectedItem(); item != nil {
				templateItem := item.(*TemplateItem)
				return ActionSelectTemplate{Template: templateItem.Template}
			}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (t *Templates) Cursor() *tea.Cursor {
	return nil
}

// Draw implements [Dialog].
func (t *Templates) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	s := t.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-s.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-s.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - s.Dialog.View.GetHorizontalFrameSize()
	heightOffset := s.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		s.Dialog.HelpView.GetVerticalFrameSize() +
		s.Dialog.View.GetVerticalFrameSize()
	t.list.SetSize(innerWidth, height-heightOffset)
	t.help.SetWidth(innerWidth)

	rc := NewRenderContext(s, width)
	rc.Title = "Select Template"
	listView := s.Dialog.List.Height(t.list.Height()).Render(t.list.Render())
	rc.AddPart(listView)
	rc.Help = t.help.View(t)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, nil)
	return nil
}

// ShortHelp implements [help.KeyMap].
func (t *Templates) ShortHelp() []key.Binding {
	return []key.Binding{
		t.keyMap.UpDown,
		t.keyMap.Select,
		t.keyMap.Skip,
		t.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (t *Templates) FullHelp() [][]key.Binding {
	return [][]key.Binding{{
		t.keyMap.UpDown,
		t.keyMap.Select,
		t.keyMap.Skip,
		t.keyMap.Close,
	}}
}

// TemplateItem wraps a Template to implement the ListItem interface.
type TemplateItem struct {
	Template *templates.Template
	t        *styles.Styles
	m        fuzzy.Match
	cache    map[int]string
	focused  bool
}

var _ ListItem = &TemplateItem{}

// Filter returns the filterable value of the template.
func (ti *TemplateItem) Filter() string {
	return ti.Template.Name + " " + ti.Template.Description
}

// ID returns the unique identifier of the template.
func (ti *TemplateItem) ID() string {
	return ti.Template.Name
}

// SetMatch sets the fuzzy match for the template item.
func (ti *TemplateItem) SetMatch(m fuzzy.Match) {
	ti.cache = nil
	ti.m = m
}

// SetFocused sets the focus state of the template item.
func (ti *TemplateItem) SetFocused(focused bool) {
	if ti.focused != focused {
		ti.cache = nil
	}
	ti.focused = focused
}

// Render returns the string representation of the template item.
func (ti *TemplateItem) Render(width int) string {
	styles := ListItemStyles{
		ItemBlurred:     ti.t.Dialog.NormalItem,
		ItemFocused:     ti.t.Dialog.SelectedItem,
		InfoTextBlurred: ti.t.Subtle,
		InfoTextFocused: ti.t.Base,
	}

	// Truncate description to fit
	desc := ti.Template.Description
	if len(desc) > 40 {
		desc = desc[:37] + "..."
	}

	return renderItem(styles, ti.Template.Name, desc, ti.focused, width, ti.cache, &ti.m)
}

// templateItems converts templates to list items.
func templateItems(t *styles.Styles, tmpls ...*templates.Template) []list.FilterableItem {
	items := make([]list.FilterableItem, len(tmpls))
	for i, tmpl := range tmpls {
		items[i] = &TemplateItem{Template: tmpl, t: t}
	}
	return items
}

// Truncate helper for display.
func truncate(s string, max int) string {
	return ansi.Truncate(s, max, "…")
}
