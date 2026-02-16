package prompt

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/charmbracelet/crush/internal/skills"
)

// MemoryReader provides access to learned memories for prompt building.
type MemoryReader interface {
	Recent(ctx context.Context, limit int) ([]MemoryEntry, error)
}

// MemoryEntry represents a single memory for prompt building.
type MemoryEntry struct {
	Category string
	Content  string
}

// SummaryReader provides access to past session summaries for prompt building.
type SummaryReader interface {
	Recent(ctx context.Context, limit int) ([]SummaryEntry, error)
}

// SummaryEntry represents a past session summary for prompt building.
type SummaryEntry struct {
	SessionID string
	Summary   string
}

// Prompt represents a template-based prompt generator.
type Prompt struct {
	name          string
	template      string
	now           func() time.Time
	platform      string
	workingDir    string
	memoryReader  MemoryReader
	summaryReader SummaryReader
}

type PromptDat struct {
	Provider         string
	Model            string
	Config           config.Config
	WorkingDir       string
	IsGitRepo        bool
	Platform         string
	DateTime         string
	GitStatus        string
	ContextFiles     []ContextFile
	AvailSkillXML    string
	LearnedMemories  string
	RecentSummaries  string
}

type ContextFile struct {
	Path    string
	Content string
}

type Option func(*Prompt)

func WithTimeFunc(fn func() time.Time) Option {
	return func(p *Prompt) {
		p.now = fn
	}
}

func WithPlatform(platform string) Option {
	return func(p *Prompt) {
		p.platform = platform
	}
}

func WithWorkingDir(workingDir string) Option {
	return func(p *Prompt) {
		p.workingDir = workingDir
	}
}

func WithMemoryReader(mr MemoryReader) Option {
	return func(p *Prompt) {
		p.memoryReader = mr
	}
}

func WithSummaryReader(sr SummaryReader) Option {
	return func(p *Prompt) {
		p.summaryReader = sr
	}
}

func NewPrompt(name, promptTemplate string, opts ...Option) (*Prompt, error) {
	p := &Prompt{
		name:     name,
		template: promptTemplate,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *Prompt) Build(ctx context.Context, provider, model string, cfg config.Config) (string, error) {
	t, err := template.New(p.name).Parse(p.template)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var sb strings.Builder
	d, err := p.promptData(ctx, provider, model, cfg)
	if err != nil {
		return "", err
	}
	if err := t.Execute(&sb, d); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return sb.String(), nil
}

func processFile(filePath string) *ContextFile {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	return &ContextFile{
		Path:    filePath,
		Content: string(content),
	}
}

func processContextPath(p string, cfg config.Config) []ContextFile {
	var contexts []ContextFile
	fullPath := p
	if !filepath.IsAbs(p) {
		fullPath = filepath.Join(cfg.WorkingDir(), p)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return contexts
	}
	if info.IsDir() {
		filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if result := processFile(path); result != nil {
					contexts = append(contexts, *result)
				}
			}
			return nil
		})
	} else {
		result := processFile(fullPath)
		if result != nil {
			contexts = append(contexts, *result)
		}
	}
	return contexts
}

// expandPath expands ~ and environment variables in file paths
func expandPath(path string, cfg config.Config) string {
	path = home.Long(path)
	// Handle environment variable expansion using the same pattern as config
	if strings.HasPrefix(path, "$") {
		if expanded, err := cfg.Resolver().ResolveValue(path); err == nil {
			path = expanded
		}
	}

	return path
}

func (p *Prompt) promptData(ctx context.Context, provider, model string, cfg config.Config) (PromptDat, error) {
	workingDir := cmp.Or(p.workingDir, cfg.WorkingDir())
	platform := cmp.Or(p.platform, runtime.GOOS)

	files := map[string][]ContextFile{}

	for _, pth := range cfg.Options.ContextPaths {
		expanded := expandPath(pth, cfg)
		pathKey := strings.ToLower(expanded)
		if _, ok := files[pathKey]; ok {
			continue
		}
		content := processContextPath(expanded, cfg)
		files[pathKey] = content
	}

	// Discover and load skills metadata.
	var availSkillXML string
	if len(cfg.Options.SkillsPaths) > 0 {
		expandedPaths := make([]string, 0, len(cfg.Options.SkillsPaths))
		for _, pth := range cfg.Options.SkillsPaths {
			expandedPaths = append(expandedPaths, expandPath(pth, cfg))
		}
		if discoveredSkills := skills.Discover(expandedPaths); len(discoveredSkills) > 0 {
			availSkillXML = skills.ToPromptXML(discoveredSkills)
		}
	}

	// Load learned memories if available.
	var learnedMemories string
	if p.memoryReader != nil {
		memories, err := p.memoryReader.Recent(ctx, 50)
		if err != nil {
			slog.Warn("Failed to load memories for prompt", "error", err)
		} else {
			learnedMemories = formatMemoriesForPrompt(memories)
		}
	}

	// Load recent session summaries if available.
	var recentSummaries string
	if p.summaryReader != nil {
		summaries, err := p.summaryReader.Recent(ctx, 5)
		if err != nil {
			slog.Warn("Failed to load summaries for prompt", "error", err)
		} else {
			recentSummaries = formatSummariesForPrompt(summaries)
		}
	}

	isGit := isGitRepo(cfg.WorkingDir())
	data := PromptDat{
		Provider:        provider,
		Model:           model,
		Config:          cfg,
		WorkingDir:      filepath.ToSlash(workingDir),
		IsGitRepo:       isGit,
		Platform:        platform,
		DateTime:        p.now().Format("1/2/2006 3:04 PM"),
		AvailSkillXML:   availSkillXML,
		LearnedMemories: learnedMemories,
		RecentSummaries: recentSummaries,
	}
	if isGit {
		var err error
		data.GitStatus, err = getGitStatus(ctx, cfg.WorkingDir())
		if err != nil {
			return PromptDat{}, err
		}
	}

	for _, contextFiles := range files {
		data.ContextFiles = append(data.ContextFiles, contextFiles...)
	}
	return data, nil
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func getGitStatus(ctx context.Context, dir string) (string, error) {
	sh := shell.NewShell(&shell.Options{
		WorkingDir: dir,
	})
	branch, err := getGitBranch(ctx, sh)
	if err != nil {
		return "", err
	}
	status, err := getGitStatusSummary(ctx, sh)
	if err != nil {
		return "", err
	}
	commits, err := getGitRecentCommits(ctx, sh)
	if err != nil {
		return "", err
	}
	return branch + status + commits, nil
}

func getGitBranch(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git branch --show-current 2>/dev/null")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", nil
	}
	return fmt.Sprintf("Current branch: %s\n", out), nil
}

func getGitStatusSummary(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git status --short 2>/dev/null | head -20")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "Status: clean\n", nil
	}
	return fmt.Sprintf("Status:\n%s\n", out), nil
}

func getGitRecentCommits(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git log --oneline -n 3 2>/dev/null")
	if err != nil || out == "" {
		return "", nil
	}
	out = strings.TrimSpace(out)
	return fmt.Sprintf("Recent commits:\n%s\n", out), nil
}

// formatMemoriesForPrompt formats learned memories for inclusion in the prompt.
func formatMemoriesForPrompt(entries []MemoryEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<learned_memories>\n")
	sb.WriteString("These are things you've learned across previous sessions. Use them to inform your work.\n\n")

	// Group by category.
	categories := make(map[string][]MemoryEntry)
	for _, e := range entries {
		categories[e.Category] = append(categories[e.Category], e)
	}

	for category, memories := range categories {
		sb.WriteString(fmt.Sprintf("## %s\n", category))
		for _, m := range memories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("</learned_memories>")
	return sb.String()
}

// formatSummariesForPrompt formats recent session summaries for inclusion in the prompt.
func formatSummariesForPrompt(entries []SummaryEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<past_sessions>\n")
	sb.WriteString("These are summaries from PREVIOUS sessions. They are NOT part of the current conversation.\n")
	sb.WriteString("Reference them if clearly relevant or if the user asks about past work.\n")
	sb.WriteString("Some may relate to ongoing projects; use judgment to connect context when appropriate.\n\n")

	for i, e := range entries {
		sessionLabel := e.SessionID
		if len(sessionLabel) > 8 {
			sessionLabel = sessionLabel[:8]
		}
		sb.WriteString(fmt.Sprintf("### Past Session %d (%s)\n", i+1, sessionLabel))
		sb.WriteString(e.Summary)
		sb.WriteString("\n\n---\n\n")
	}

	sb.WriteString("</past_sessions>")
	return sb.String()
}

func (p *Prompt) Name() string {
	return p.name
}
