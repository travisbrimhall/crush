package attachments

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/message"
)

type Keymap struct {
	DeleteMode,
	DeleteAll,
	Escape key.Binding
}

func New(renderer *Renderer, keyMap Keymap) *Attachments {
	return &Attachments{
		keyMap:   keyMap,
		renderer: renderer,
	}
}

type Attachments struct {
	renderer *Renderer
	keyMap   Keymap
	list     []message.Attachment
	deleting bool
}

func (m *Attachments) List() []message.Attachment { return m.list }
func (m *Attachments) Reset()                     { m.list = nil }

func (m *Attachments) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case message.Attachment:
		m.list = append(m.list, msg)
		return true
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.DeleteMode):
			if len(m.list) > 0 {
				m.deleting = true
			}
			return true
		case m.deleting && key.Matches(msg, m.keyMap.Escape):
			m.deleting = false
			return true
		case m.deleting && key.Matches(msg, m.keyMap.DeleteAll):
			m.deleting = false
			m.list = nil
			return true
		case m.deleting:
			// Handle digit keys for individual attachment deletion.
			r := msg.Code
			if r >= '0' && r <= '9' {
				num := int(r - '0')
				if num < len(m.list) {
					m.list = slices.Delete(m.list, num, num+1)
				}
				m.deleting = false
			}
			return true
		}
	}
	return false
}

func (m *Attachments) Render(width int) string {
	return m.renderer.Render(m.list, m.deleting, width)
}

func NewRenderer(normalStyle, deletingStyle lipgloss.Style, icons IconStyles) *Renderer {
	return &Renderer{
		normalStyle:   normalStyle,
		deletingStyle: deletingStyle,
		icons:         icons,
	}
}

// IconStyles holds styles for different file type icons.
type IconStyles struct {
	Image, Text, Code, Config, Archive, Audio, Video, PDF, Data, File lipgloss.Style
	// Source-specific icons for external integrations.
	VSCode, Chrome, Docker lipgloss.Style
}

type Renderer struct {
	normalStyle, deletingStyle lipgloss.Style
	icons                      IconStyles
}

func (r *Renderer) Render(attachments []message.Attachment, deleting bool, width int) string {
	var chips []string

	// Estimate max item width using a reasonable filename length.
	maxItemWidth := lipgloss.Width(r.icons.Image.String() + r.normalStyle.Render(strings.Repeat("x", 30)))
	fits := int(math.Floor(float64(width)/float64(maxItemWidth))) - 1

	for i, att := range attachments {
		label := r.formatLabel(att)

		if deleting {
			chips = append(
				chips,
				r.deletingStyle.Render(fmt.Sprintf("%d", i)),
				r.normalStyle.Render(label),
			)
		} else {
			chips = append(
				chips,
				r.icon(att).String(),
				r.normalStyle.Render(label),
			)
		}

		if i == fits && len(attachments) > i {
			chips = append(chips, lipgloss.NewStyle().Width(maxItemWidth).Render(fmt.Sprintf("%d more…", len(attachments)-fits)))
			break
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Left, chips...)
}

// formatLabel returns the display label for an attachment.
// For selections with line ranges, shows "filename:start-end".
// For single-line selections, shows "filename:line".
// Otherwise, shows just the filename.
func (r *Renderer) formatLabel(att message.Attachment) string {
	filename := filepath.Base(att.FileName)
	if att.StartLine > 0 && att.EndLine > 0 {
		if att.StartLine == att.EndLine {
			return fmt.Sprintf("%s:%d", filename, att.StartLine)
		}
		return fmt.Sprintf("%s:%d-%d", filename, att.StartLine, att.EndLine)
	}
	return filename
}

func (r *Renderer) icon(a message.Attachment) lipgloss.Style {
	// Source-specific icons take priority.
	switch a.Source {
	case message.AttachmentSourceVSCode:
		return r.icons.VSCode
	case message.AttachmentSourceChrome:
		return r.icons.Chrome
	case message.AttachmentSourceDocker:
		return r.icons.Docker
	}

	ext := strings.ToLower(filepath.Ext(a.FileName))

	// Images.
	if a.IsImage() {
		return r.icons.Image
	}

	// Code files.
	switch ext {
	case ".go", ".js", ".ts", ".tsx", ".jsx", ".py", ".rb", ".rs", ".c", ".cpp", ".h",
		".java", ".kt", ".swift", ".cs", ".php", ".sh", ".bash", ".zsh", ".fish",
		".lua", ".pl", ".r", ".scala", ".zig", ".nim", ".ex", ".exs", ".erl",
		".hs", ".ml", ".fs", ".clj", ".lisp", ".el", ".vim", ".sql":
		return r.icons.Code
	}

	// Config files.
	switch ext {
	case ".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".env",
		".xml", ".plist", ".properties":
		return r.icons.Config
	}

	// Archive files.
	switch ext {
	case ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar", ".tgz":
		return r.icons.Archive
	}

	// Audio files.
	switch ext {
	case ".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma":
		return r.icons.Audio
	}

	// Video files.
	switch ext {
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".flv", ".wmv":
		return r.icons.Video
	}

	// PDF.
	if ext == ".pdf" {
		return r.icons.PDF
	}

	// Data files.
	switch ext {
	case ".csv", ".tsv", ".xls", ".xlsx", ".parquet", ".db", ".sqlite":
		return r.icons.Data
	}

	// Text/document files.
	switch ext {
	case ".txt", ".md", ".markdown", ".rst", ".adoc", ".org", ".tex", ".rtf", ".doc", ".docx":
		return r.icons.Text
	}

	// Default fallback.
	return r.icons.File
}
