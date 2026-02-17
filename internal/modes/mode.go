// Package modes provides context mode switching for the agent.
// Modes allow pre-loading relevant memories, context files, and instructions
// for specific types of work (e.g., infra, frontend, debugging).
package modes

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charlievieth/fastwalk"
	"gopkg.in/yaml.v3"
)

const (
	ModeFileName   = "MODE.md"
	MaxNameLength  = 64
	MaxDescLength  = 256
)

// Mode represents a context mode configuration.
type Mode struct {
	Name           string   `yaml:"name" json:"name"`
	Description    string   `yaml:"description" json:"description"`
	MemoryTags     []string `yaml:"memory_tags,omitempty" json:"memory_tags,omitempty"`
	ContextFiles   []string `yaml:"context_files,omitempty" json:"context_files,omitempty"`
	Instructions   string   `yaml:"-" json:"instructions"`
	Path           string   `yaml:"-" json:"path"`
	ModeFilePath   string   `yaml:"-" json:"mode_file_path"`
}

// Validate checks if the mode configuration is valid.
func (m *Mode) Validate() error {
	var errs []error

	if m.Name == "" {
		errs = append(errs, errors.New("name is required"))
	} else if len(m.Name) > MaxNameLength {
		errs = append(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
	}

	if m.Description == "" {
		errs = append(errs, errors.New("description is required"))
	} else if len(m.Description) > MaxDescLength {
		errs = append(errs, fmt.Errorf("description exceeds %d characters", MaxDescLength))
	}

	return errors.Join(errs...)
}

// LoadContextFiles reads all context files for this mode and returns their combined content.
func (m *Mode) LoadContextFiles() (string, error) {
	if len(m.ContextFiles) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, cf := range m.ContextFiles {
		// Resolve relative to mode directory
		path := cf
		if !filepath.IsAbs(cf) {
			path = filepath.Join(m.Path, cf)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to load mode context file", "path", path, "error", err)
			continue
		}

		sb.WriteString(fmt.Sprintf("### %s\n\n", filepath.Base(cf)))
		sb.Write(content)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// Parse parses a MODE.md file.
func Parse(path string) (*Mode, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	frontmatter, body, err := splitFrontmatter(string(content))
	if err != nil {
		return nil, err
	}

	var mode Mode
	if err := yaml.Unmarshal([]byte(frontmatter), &mode); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	mode.Instructions = strings.TrimSpace(body)
	mode.Path = filepath.Dir(path)
	mode.ModeFilePath = path

	return &mode, nil
}

// splitFrontmatter extracts YAML frontmatter and body from markdown content.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", "", errors.New("no YAML frontmatter found")
	}

	rest := strings.TrimPrefix(content, "---\n")
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return "", "", errors.New("unclosed frontmatter")
	}

	return before, after, nil
}

// Discover finds all valid modes in the given paths.
func Discover(paths []string) []*Mode {
	var modes []*Mode
	var mu sync.Mutex
	seen := make(map[string]bool)

	for _, base := range paths {
		conf := fastwalk.Config{
			Follow:  true,
			ToSlash: fastwalk.DefaultToSlash(),
		}
		fastwalk.Walk(&conf, base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() || d.Name() != ModeFileName {
				return nil
			}
			mu.Lock()
			if seen[path] {
				mu.Unlock()
				return nil
			}
			seen[path] = true
			mu.Unlock()

			mode, err := Parse(path)
			if err != nil {
				slog.Warn("Failed to parse mode file", "path", path, "error", err)
				return nil
			}
			if err := mode.Validate(); err != nil {
				slog.Warn("Mode validation failed", "path", path, "error", err)
				return nil
			}
			slog.Debug("Successfully loaded mode", "name", mode.Name, "path", path)
			mu.Lock()
			modes = append(modes, mode)
			mu.Unlock()
			return nil
		})
	}

	return modes
}

// FindByName returns the mode with the given name, or nil if not found.
func FindByName(modes []*Mode, name string) *Mode {
	name = strings.ToLower(name)
	for _, m := range modes {
		if strings.ToLower(m.Name) == name {
			return m
		}
	}
	return nil
}

// ToListString generates a human-readable list of available modes.
func ToListString(modes []*Mode) string {
	if len(modes) == 0 {
		return "No modes available."
	}

	var sb strings.Builder
	sb.WriteString("Available modes:\n")
	for _, m := range modes {
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", m.Name, m.Description))
	}
	return sb.String()
}
