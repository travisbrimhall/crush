package message

import (
	"slices"
	"strings"
)

// AttachmentSource indicates where an attachment originated from.
type AttachmentSource string

const (
	// AttachmentSourceLocal indicates the attachment was added locally (drag-drop, file picker).
	AttachmentSourceLocal AttachmentSource = ""
	// AttachmentSourceVSCode indicates the attachment came from VS Code.
	AttachmentSourceVSCode AttachmentSource = "vscode"
	// AttachmentSourceChrome indicates the attachment came from Chrome DevTools.
	AttachmentSourceChrome AttachmentSource = "chrome"
	// AttachmentSourceDocker indicates the attachment came from Docker.
	AttachmentSourceDocker AttachmentSource = "docker"
)

type Attachment struct {
	FilePath string
	FileName string
	MimeType string
	Content  []byte
	Source   AttachmentSource
	// StartLine and EndLine are set for code selections (1-indexed, inclusive).
	// Zero values indicate this is not a line-range selection.
	StartLine int
	EndLine   int
}

func (a Attachment) IsText() bool  { return strings.HasPrefix(a.MimeType, "text/") }
func (a Attachment) IsImage() bool { return strings.HasPrefix(a.MimeType, "image/") }

// ContainsTextAttachment returns true if any of the attachments is a text attachment.
func ContainsTextAttachment(attachments []Attachment) bool {
	return slices.ContainsFunc(attachments, func(a Attachment) bool {
		return a.IsText()
	})
}
