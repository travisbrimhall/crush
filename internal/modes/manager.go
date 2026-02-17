package modes

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/charmbracelet/crush/internal/memory"
)

// Manager handles mode discovery, state, and activation.
type Manager struct {
	paths       []string
	modes       []*Mode
	mu          sync.RWMutex
	active      map[string]*Mode // sessionID -> active mode
	memoryStore memory.MemoryStore
}

// NewManager creates a mode manager that discovers modes from the given paths.
func NewManager(paths []string, memoryStore memory.MemoryStore) *Manager {
	// Check for nil interface or interface holding nil pointer.
	if memoryStore != nil && reflect.ValueOf(memoryStore).IsNil() {
		memoryStore = nil
	}
	m := &Manager{
		paths:       paths,
		active:      make(map[string]*Mode),
		memoryStore: memoryStore,
	}
	m.Refresh()
	return m
}

// Refresh re-discovers modes from configured paths.
func (m *Manager) Refresh() {
	modes := Discover(m.paths)
	m.mu.Lock()
	m.modes = modes
	m.mu.Unlock()
}

// List returns all discovered modes.
func (m *Manager) List() []*Mode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modes
}

// Get returns a mode by name.
func (m *Manager) Get(name string) *Mode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return FindByName(m.modes, name)
}

// Activate sets the active mode for a session and returns the formatted context.
func (m *Manager) Activate(ctx context.Context, sessionID, modeName string) (string, error) {
	mode := m.Get(modeName)
	if mode == nil {
		return "", fmt.Errorf("mode %q not found. Available: %s", modeName, m.listNames())
	}

	m.mu.Lock()
	m.active[sessionID] = mode
	m.mu.Unlock()

	// Build the mode context string.
	return m.buildModeContext(ctx, mode)
}

// Deactivate clears the active mode for a session.
func (m *Manager) Deactivate(sessionID string) {
	m.mu.Lock()
	delete(m.active, sessionID)
	m.mu.Unlock()
}

// Active returns the active mode for a session, or nil if none.
func (m *Manager) Active(sessionID string) *Mode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active[sessionID]
}

// ActiveContext returns the formatted context for the active mode, or empty if none.
func (m *Manager) ActiveContext(ctx context.Context, sessionID string) string {
	mode := m.Active(sessionID)
	if mode == nil {
		return ""
	}
	context, _ := m.buildModeContext(ctx, mode)
	return context
}

func (m *Manager) buildModeContext(ctx context.Context, mode *Mode) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("<active_mode name=%q>\n", mode.Name))
	sb.WriteString(fmt.Sprintf("You are in **%s** mode. %s\n\n", mode.Name, mode.Description))

	// Add mode instructions.
	if mode.Instructions != "" {
		sb.WriteString("## Mode Instructions\n\n")
		sb.WriteString(mode.Instructions)
		sb.WriteString("\n\n")
	}

	// Load and add context files.
	contextDocs, err := mode.LoadContextFiles()
	if err == nil && contextDocs != "" {
		sb.WriteString("## Context Documents\n\n")
		sb.WriteString(contextDocs)
	}

	// Load tagged memories if we have a memory store and tags.
	if m.memoryStore != nil && len(mode.MemoryTags) > 0 {
		memories := m.loadTaggedMemories(ctx, mode.MemoryTags)
		if memories != "" {
			sb.WriteString("## Relevant Memories\n\n")
			sb.WriteString(memories)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("</active_mode>")
	return sb.String(), nil
}

func (m *Manager) loadTaggedMemories(ctx context.Context, tags []string) string {
	var sb strings.Builder
	for _, tag := range tags {
		entries, err := m.memoryStore.List(ctx, tag)
		if err != nil {
			continue
		}
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", e.Category, e.Content))
		}
	}
	return sb.String()
}

func (m *Manager) listNames() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.modes) == 0 {
		return "(none)"
	}

	names := make([]string, len(m.modes))
	for i, mode := range m.modes {
		names[i] = mode.Name
	}
	return strings.Join(names, ", ")
}
