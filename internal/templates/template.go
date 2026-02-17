// Package templates provides session templates for the agent.
// Templates pre-load relevant context files and instructions
// when starting a new session for specific types of work.
package templates

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
	TemplateFileName = "TEMPLATE.md"
	MaxNameLength    = 64
	MaxDescLength    = 256
)

// Template represents a session template configuration.
type Template struct {
	Name         string   `yaml:"name" json:"name"`
	Description  string   `yaml:"description" json:"description"`
	MemoryTags   []string `yaml:"memory_tags,omitempty" json:"memory_tags,omitempty"`
	ContextFiles []string `yaml:"context_files,omitempty" json:"context_files,omitempty"`
	Instructions string   `yaml:"-" json:"instructions"`
	Path         string   `yaml:"-" json:"path"`
	FilePath     string   `yaml:"-" json:"file_path"`
}

// Validate checks if the template configuration is valid.
func (t *Template) Validate() error {
	var errs []error

	if t.Name == "" {
		errs = append(errs, errors.New("name is required"))
	} else if len(t.Name) > MaxNameLength {
		errs = append(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
	}

	if t.Description == "" {
		errs = append(errs, errors.New("description is required"))
	} else if len(t.Description) > MaxDescLength {
		errs = append(errs, fmt.Errorf("description exceeds %d characters", MaxDescLength))
	}

	return errors.Join(errs...)
}

// LoadContextFiles reads all context files for this template and returns their combined content.
func (t *Template) LoadContextFiles() (string, error) {
	if len(t.ContextFiles) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, cf := range t.ContextFiles {
		// Resolve relative to template directory
		path := cf
		if !filepath.IsAbs(cf) {
			path = filepath.Join(t.Path, cf)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to load template context file", "path", path, "error", err)
			continue
		}

		sb.WriteString(fmt.Sprintf("### %s\n\n", filepath.Base(cf)))
		sb.Write(content)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// Parse parses a TEMPLATE.md file.
func Parse(path string) (*Template, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	frontmatter, body, err := splitFrontmatter(string(content))
	if err != nil {
		return nil, err
	}

	var tmpl Template
	if err := yaml.Unmarshal([]byte(frontmatter), &tmpl); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	tmpl.Instructions = strings.TrimSpace(body)
	tmpl.Path = filepath.Dir(path)
	tmpl.FilePath = path

	return &tmpl, nil
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

// Discover finds all valid templates in the given paths.
func Discover(paths []string) []*Template {
	var templates []*Template
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
			if d.IsDir() || d.Name() != TemplateFileName {
				return nil
			}
			mu.Lock()
			if seen[path] {
				mu.Unlock()
				return nil
			}
			seen[path] = true
			mu.Unlock()

			tmpl, err := Parse(path)
			if err != nil {
				slog.Warn("Failed to parse template file", "path", path, "error", err)
				return nil
			}
			if err := tmpl.Validate(); err != nil {
				slog.Warn("Template validation failed", "path", path, "error", err)
				return nil
			}
			slog.Debug("Successfully loaded template", "name", tmpl.Name, "path", path)
			mu.Lock()
			templates = append(templates, tmpl)
			mu.Unlock()
			return nil
		})
	}

	return templates
}

// FindByName returns the template with the given name, or nil if not found.
func FindByName(templates []*Template, name string) *Template {
	name = strings.ToLower(name)
	for _, t := range templates {
		if strings.ToLower(t.Name) == name {
			return t
		}
	}
	return nil
}

// ToListString generates a human-readable list of available templates.
func ToListString(templates []*Template) string {
	if len(templates) == 0 {
		return "No templates available."
	}

	var sb strings.Builder
	sb.WriteString("Available session templates:\n")
	for _, t := range templates {
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", t.Name, t.Description))
	}
	return sb.String()
}
