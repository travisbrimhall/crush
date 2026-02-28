// Package context provides the external communications core for Crush.
// It implements a bounded, workspace-scoped, deduplicating failure snapshot
// store with a controlled execution gate.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Schema and hash versioning for forward compatibility.
const (
	SchemaVersion = "1.0"
	HashVersion   = "1"
)

// Buffer limits.
const (
	MaxContexts     = 50
	MaxTotalBytes   = 20 * 1024 * 1024 // 20MB
	MaxPayloadBytes = 1 * 1024 * 1024  // 1MB per payload
)

// Prompt assembly limits.
const (
	MaxPromptTokens     = 120_000
	MaxContextsInPrompt = 20
)

// Source identifies where a context came from.
type Source string

const (
	SourceVSCode Source = "vscode"
	SourceChrome Source = "chrome"
	SourceDocker Source = "docker"
)

// EventType identifies the type of failure event.
type EventType string

const (
	EventDiagnostic       EventType = "diagnostic_fix_request"
	EventTestFailure      EventType = "test_failure_fix_request"
	EventConsoleError     EventType = "console_error_fix_request"
	EventContainerFailure EventType = "container_failure_fix_request"
)

// Range represents a text range in a file.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position represents a position in a file.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Workspace identifies the source workspace.
type Workspace struct {
	ID   string `json:"id"`   // SHA256(absoluteRootPath)[:16]
	Root string `json:"root"` // Absolute path
	Name string `json:"name"` // Display name
}

// Entry represents a single context entry in the buffer.
type Entry struct {
	ID          string          `json:"id"`
	Hash        string          `json:"hash"`
	EventType   EventType       `json:"eventType"`
	Source      Source          `json:"source"`
	WorkspaceID string          `json:"workspaceId"`
	FilePath    string          `json:"filePath,omitempty"`
	FileVersion int             `json:"fileVersion,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	ReceivedAt  time.Time       `json:"receivedAt"`
	Count       int             `json:"count"` // Incremented on duplicate
	SizeBytes   int             `json:"sizeBytes"`
}

// Session holds the state for a single Crush session.
type Session struct {
	mu          sync.RWMutex
	workspaceID string
	contexts    []*Entry
	contextMap  map[string]*Entry // hash -> entry for O(1) dedup lookup
	totalBytes  int64
	isRunning   bool
	token       string // CSRF session token
}

// NewSession creates a new session with the given CSRF token.
func NewSession(token string) *Session {
	return &Session{
		contexts:   make([]*Entry, 0, MaxContexts),
		contextMap: make(map[string]*Entry, MaxContexts),
		token:      token,
	}
}

// Token returns the CSRF session token.
func (s *Session) Token() string {
	return s.token
}

// WorkspaceID returns the bound workspace ID, or empty if not yet bound.
func (s *Session) WorkspaceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workspaceID
}

// IsRunning returns whether an agent run is in progress.
func (s *Session) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// ContextCount returns the number of contexts in the buffer.
func (s *Session) ContextCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.contexts)
}

// TotalBytes returns the total size of all contexts.
func (s *Session) TotalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytes
}

// Contexts returns a snapshot of all contexts (newest first).
func (s *Session) Contexts() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return copy sorted newest first
	result := make([]*Entry, len(s.contexts))
	for i, ctx := range s.contexts {
		result[len(s.contexts)-1-i] = ctx
	}
	return result
}

// HashPayload computes the deduplication hash for a payload.
// Includes versioning so hash algorithm changes don't cause collisions.
func HashPayload(eventType EventType, filePath string, payload json.RawMessage) string {
	// Extract key fields for hashing based on event type.
	// This is intentionally coarse - we hash the important identifying fields.
	var hashInput struct {
		Version       string    `json:"v"`
		EventType     EventType `json:"e"`
		FilePath      string    `json:"f"`
		DiagCode      string    `json:"dc,omitempty"`
		DiagMessage   string    `json:"dm,omitempty"`
		RangeStart    int       `json:"rs,omitempty"`
		RangeEnd      int       `json:"re,omitempty"`
		ExitCode      int       `json:"ec,omitempty"`
		ExceptionType string    `json:"et,omitempty"`
		ErrorMessage  string    `json:"em,omitempty"`
	}

	hashInput.Version = HashVersion
	hashInput.EventType = eventType
	hashInput.FilePath = filePath

	// Parse payload to extract hashable fields.
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err == nil {
		// VS Code diagnostic fields
		if diag, ok := p["diagnostic"].(map[string]any); ok {
			if code, ok := diag["code"]; ok {
				hashInput.DiagCode = fmt.Sprintf("%v", code)
			}
			if msg, ok := diag["message"].(string); ok {
				hashInput.DiagMessage = msg
			}
			if r, ok := diag["range"].(map[string]any); ok {
				if start, ok := r["start"].(map[string]any); ok {
					if line, ok := start["line"].(float64); ok {
						hashInput.RangeStart = int(line)
					}
				}
				if end, ok := r["end"].(map[string]any); ok {
					if line, ok := end["line"].(float64); ok {
						hashInput.RangeEnd = int(line)
					}
				}
			}
		}

		// Docker failure fields
		if failure, ok := p["failure"].(map[string]any); ok {
			if ec, ok := failure["exitCode"].(float64); ok {
				hashInput.ExitCode = int(ec)
			}
		}

		// Chrome error fields
		if err, ok := p["error"].(map[string]any); ok {
			if msg, ok := err["message"].(string); ok {
				hashInput.ErrorMessage = msg
			}
		}
		if exc, ok := p["exception"].(map[string]any); ok {
			if cls, ok := exc["className"].(string); ok {
				hashInput.ExceptionType = cls
			}
		}
	}

	data, _ := json.Marshal(hashInput)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
