package templates

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/charmbracelet/crush/internal/memory"
)

// Store manages template discovery and provides context building.
type Store struct {
	paths       []string
	templates   []*Template
	mu          sync.RWMutex
	memoryStore memory.MemoryStore
}

// NewStore creates a template store that discovers templates from the given paths.
func NewStore(paths []string, memoryStore memory.MemoryStore) *Store {
	// Check for nil interface or interface holding nil pointer.
	if memoryStore != nil && reflect.ValueOf(memoryStore).IsNil() {
		memoryStore = nil
	}
	s := &Store{
		paths:       paths,
		memoryStore: memoryStore,
	}
	s.Refresh()
	return s
}

// Refresh re-discovers templates from configured paths.
func (s *Store) Refresh() {
	templates := Discover(s.paths)
	s.mu.Lock()
	s.templates = templates
	s.mu.Unlock()
}

// List returns all discovered templates.
func (s *Store) List() []*Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.templates
}

// Get returns a template by name.
func (s *Store) Get(name string) *Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return FindByName(s.templates, name)
}

// BuildContext builds the initial context string for a template.
// This is injected into the session's first system message.
func (s *Store) BuildContext(ctx context.Context, tmpl *Template) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("<session_template name=%q>\n", tmpl.Name))
	sb.WriteString(fmt.Sprintf("This session was started from the **%s** template.\n", tmpl.Name))
	sb.WriteString(fmt.Sprintf("%s\n\n", tmpl.Description))

	// Add template instructions.
	if tmpl.Instructions != "" {
		sb.WriteString("## Instructions\n\n")
		sb.WriteString(tmpl.Instructions)
		sb.WriteString("\n\n")
	}

	// Load and add context files.
	contextDocs, err := tmpl.LoadContextFiles()
	if err == nil && contextDocs != "" {
		sb.WriteString("## Context Documents\n\n")
		sb.WriteString(contextDocs)
	}

	// Load tagged memories if we have a memory store and tags.
	if s.memoryStore != nil && len(tmpl.MemoryTags) > 0 {
		memories := s.loadTaggedMemories(ctx, tmpl.MemoryTags)
		if memories != "" {
			sb.WriteString("## Relevant Memories\n\n")
			sb.WriteString(memories)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("</session_template>")
	return sb.String(), nil
}

func (s *Store) loadTaggedMemories(ctx context.Context, tags []string) string {
	var sb strings.Builder
	for _, tag := range tags {
		entries, err := s.memoryStore.List(ctx, tag)
		if err != nil {
			continue
		}
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", e.Category, e.Content))
		}
	}
	return sb.String()
}
