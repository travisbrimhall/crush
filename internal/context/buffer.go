package context

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Errors returned by buffer operations.
var (
	ErrWorkspaceMismatch = errors.New("context workspace does not match active session")
	ErrPayloadTooLarge   = errors.New("payload exceeds maximum size")
	ErrAgentRunning      = errors.New("agent already running")
	ErrInvalidSchema     = errors.New("invalid or missing schemaVersion")
	ErrInvalidToken      = errors.New("invalid or missing session token")
)

// ContextPayload is the expected structure of incoming context requests.
type ContextPayload struct {
	SchemaVersion string          `json:"schemaVersion"`
	Event         EventType       `json:"event"`
	Source        Source          `json:"source"`
	Timestamp     string          `json:"timestamp,omitempty"` // Advisory only
	Workspace     Workspace       `json:"workspace"`
	File          *FileInfo       `json:"file,omitempty"`
	Payload       json.RawMessage `json:"-"` // The full original payload
}

// FileInfo contains file metadata from the source.
type FileInfo struct {
	Path       string `json:"path"`
	LanguageID string `json:"languageId,omitempty"`
	Version    int    `json:"version"`
}

// AddContextResult is returned from AddContext.
type AddContextResult struct {
	ContextID string `json:"contextId"`
	IsNew     bool   `json:"isNew"`
	Count     int    `json:"count"`
	Entry     *Entry `json:"-"` // The entry (for pubsub)
}

// AddContext adds a new context to the buffer, deduplicating and evicting as needed.
func (s *Session) AddContext(rawPayload []byte) (*AddContextResult, error) {
	// Check payload size first.
	if len(rawPayload) > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}

	// Parse the payload to extract metadata.
	var cp ContextPayload
	if err := json.Unmarshal(rawPayload, &cp); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// Validate schema version.
	if cp.SchemaVersion != SchemaVersion {
		return nil, ErrInvalidSchema
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate workspace - BEFORE any state mutation.
	if s.workspaceID != "" && cp.Workspace.ID != s.workspaceID {
		return nil, ErrWorkspaceMismatch
	}

	// Lazy workspace binding on first context.
	if s.workspaceID == "" {
		s.workspaceID = cp.Workspace.ID
	}

	// Compute dedup hash.
	filePath := ""
	fileVersion := 0
	if cp.File != nil {
		filePath = cp.File.Path
		fileVersion = cp.File.Version
	}
	hash := HashPayload(cp.Event, filePath, rawPayload)

	// Check for duplicate.
	if existing, ok := s.contextMap[hash]; ok {
		existing.Count++
		existing.ReceivedAt = time.Now() // Update last seen
		return &AddContextResult{
			ContextID: existing.ID,
			IsNew:     false,
			Count:     existing.Count,
			Entry:     existing,
		}, nil
	}

	// Create new entry.
	id := generateID()
	entry := &Entry{
		ID:          id,
		Hash:        hash,
		EventType:   cp.Event,
		Source:      cp.Source,
		WorkspaceID: cp.Workspace.ID,
		FilePath:    filePath,
		FileVersion: fileVersion,
		Payload:     rawPayload,
		ReceivedAt:  time.Now(),
		Count:       1,
		SizeBytes:   len(rawPayload),
	}

	// Evict until we have room (count limit).
	for len(s.contexts) >= MaxContexts {
		s.evictOldest()
	}

	// Evict until we have room (memory limit).
	for s.totalBytes+int64(entry.SizeBytes) > MaxTotalBytes && len(s.contexts) > 0 {
		s.evictOldest()
	}

	// Add the new entry.
	s.contexts = append(s.contexts, entry)
	s.contextMap[hash] = entry
	s.totalBytes += int64(entry.SizeBytes)

	return &AddContextResult{
		ContextID: id,
		IsNew:     true,
		Count:     1,
		Entry:     entry,
	}, nil
}

// evictOldest removes the oldest context from the buffer.
// Caller must hold s.mu.
func (s *Session) evictOldest() {
	if len(s.contexts) == 0 {
		return
	}

	oldest := s.contexts[0]
	s.contexts = s.contexts[1:]
	delete(s.contextMap, oldest.Hash)
	s.totalBytes -= int64(oldest.SizeBytes)
}

// Clear removes all contexts from the buffer.
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.contexts = make([]*Entry, 0, MaxContexts)
	s.contextMap = make(map[string]*Entry, MaxContexts)
	s.totalBytes = 0
}

// StartRun attempts to start an agent run. Returns error if already running.
func (s *Session) StartRun() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return ErrAgentRunning
	}
	s.isRunning = true
	return nil
}

// EndRun marks the agent run as complete.
func (s *Session) EndRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isRunning = false
}

// generateID creates a random context ID.
func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ctx_" + hex.EncodeToString(b)
}
