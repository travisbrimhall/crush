package model

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/lsp"
)

// lspWarmupMsg is sent when LSP warmup completes (or is skipped).
type lspWarmupMsg struct{}

// lspWarmup handles speculative LSP warming based on file paths detected in
// user input. It debounces rapid input changes and tracks already-warmed paths
// to avoid redundant work.
type lspWarmup struct {
	manager    *lsp.Manager
	workingDir string

	// Debounce state.
	mu            sync.Mutex
	pendingPaths  []string
	debounceTimer *time.Timer

	// Track paths we've already warmed this session.
	warmedPaths map[string]struct{}
}

// filePathPattern matches common file path patterns in user input.
// Matches paths like:
//   - internal/agent/tools/view.go
//   - ./src/main.rs
//   - /absolute/path/file.ts
//   - cmd/app/main.go:42 (with line numbers)
//   - agent.go (single file with extension)
//   - Makefile, Dockerfile, etc. (common extensionless files)
var filePathPattern = regexp.MustCompile(`(?:^|[\s"'\(])([./]?(?:[\w.-]+/)*[\w.-]+\.\w+|(?:Makefile|Dockerfile|Taskfile|Vagrantfile|Procfile|Gemfile|Rakefile|Justfile))(?::\d+)?`)

func newLSPWarmup(manager *lsp.Manager, workingDir string) *lspWarmup {
	return &lspWarmup{
		manager:     manager,
		workingDir:  workingDir,
		warmedPaths: make(map[string]struct{}),
	}
}

// detectAndWarm extracts file paths from input and schedules LSP warming.
// Returns a tea.Cmd that will warm the LSP after a debounce period.
func (w *lspWarmup) detectAndWarm(input string) tea.Cmd {
	if w.manager == nil {
		return nil
	}

	paths := w.extractPaths(input)
	if len(paths) == 0 {
		return nil
	}

	// Filter to only new paths we haven't warmed yet.
	var newPaths []string
	w.mu.Lock()
	for _, p := range paths {
		if _, warmed := w.warmedPaths[p]; !warmed {
			newPaths = append(newPaths, p)
		}
	}
	w.mu.Unlock()

	if len(newPaths) == 0 {
		return nil
	}

	return w.scheduleWarmup(newPaths)
}

// extractPaths finds file paths in the input text.
func (w *lspWarmup) extractPaths(input string) []string {
	matches := filePathPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	var paths []string

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := match[1]

		// Skip if we've already seen this path in this input.
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}

		// Resolve relative paths.
		fullPath := path
		if !strings.HasPrefix(path, "/") {
			fullPath = w.workingDir + "/" + path
		}

		// Only include paths that actually exist.
		if _, err := os.Stat(fullPath); err == nil {
			paths = append(paths, fullPath)
		}
	}

	return paths
}

// scheduleWarmup debounces warmup requests and returns a command that fires
// after the debounce period.
func (w *lspWarmup) scheduleWarmup(paths []string) tea.Cmd {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Accumulate paths.
	w.pendingPaths = append(w.pendingPaths, paths...)

	// Reset debounce timer.
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}

	// Return a command that waits for debounce then warms.
	return func() tea.Msg {
		time.Sleep(300 * time.Millisecond)
		w.flush()
		return lspWarmupMsg{}
	}
}

// flush performs the actual LSP warming for accumulated paths.
func (w *lspWarmup) flush() {
	w.mu.Lock()
	paths := w.pendingPaths
	w.pendingPaths = nil
	w.mu.Unlock()

	if len(paths) == 0 {
		return
	}

	ctx := context.Background()
	for _, path := range paths {
		w.mu.Lock()
		if _, warmed := w.warmedPaths[path]; warmed {
			w.mu.Unlock()
			continue
		}
		w.warmedPaths[path] = struct{}{}
		w.mu.Unlock()

		// Start LSP and open file.
		w.manager.Start(ctx, path)
		for client := range w.manager.Clients().Seq() {
			if client.HandlesFile(path) {
				_ = client.OpenFileOnDemand(ctx, path)
			}
		}
	}
}

// reset clears warmed paths, typically called when starting a new session.
func (w *lspWarmup) reset() {
	w.mu.Lock()
	w.warmedPaths = make(map[string]struct{})
	w.pendingPaths = nil
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
		w.debounceTimer = nil
	}
	w.mu.Unlock()
}
