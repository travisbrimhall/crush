package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/fsnotify/fsnotify"
)

// VSCodeMCPDiscoveryFile is the structure written by the VS Code extension.
type VSCodeMCPDiscoveryFile struct {
	Protocol      string `json:"protocol"`      // "mcp-http"
	Version       string `json:"version"`       // "1.0"
	Host          string `json:"host"`          // "127.0.0.1"
	Port          int    `json:"port"`          // random port
	WorkspaceRoot string `json:"workspaceRoot"` // workspace root path
	PID           int    `json:"pid"`           // VS Code extension host PID
	StartedAt     string `json:"startedAt"`     // ISO timestamp
}

const (
	vscodeMCPName          = "vscode"
	vscodeMCPDiscoveryDir  = ".crush"
	vscodeMCPDiscoveryFile = "vscode-mcp.json"
)

// VSCodeInstructions returns system prompt instructions for VS Code integration
// if VS Code MCP is connected, or an empty string if not available.
func VSCodeInstructions() string {
	state, exists := states.Get(vscodeMCPName)
	if !exists || state.State != StateConnected {
		return ""
	}

	return `<vscode-integration>
You are connected to VS Code for navigation and context (read-only).

Available actions:
- open_file: Open files in the editor
- highlight_range: Highlight code visually (use find="text" or startLine=N for easy targeting; supports style: "green" or "red")
- read_file: Get file contents (useful for unsaved buffers)
- get_diagnostics: Get compiler/linter errors and warnings
- get_definitions: Find where symbols are defined
- annotate: Add persistent inline notes to explain code (use find="text" or line=N; styles: info/warning/error)
- split_view: Open two files side-by-side
- show_diff: Compare files or show git diff

<visual-communication>
When explaining code to the user, ALWAYS prefer visual tools over text descriptions:

**Use annotate for code explanations:**
- Add inline notes directly in the editor where the user can see them in context
- Much more effective than saying "on line 42, the function does X"
- Annotations persist until you add new ones, so use them liberally
- Example: annotate with find="handleError" to point out error handling

**Use highlight_range to draw attention:**
- Highlight specific code blocks you're discussing
- Use colors meaningfully: green=good/added, red=problem/removed, yellow=focus
- Combine with annotate for rich explanations

**Preferred patterns:**
- Instead of "The bug is on line 57" → highlight_range with find="buggy code" style="red"
- Instead of "Here's how auth works..." → annotate multiple key functions with explanatory notes
- Instead of "Compare these two files" → split_view to show them side-by-side
- Instead of "Look at the changes" → show_diff to display git changes

**Tool parameters (use find= for easiest targeting):**
- highlight_range: find="text to find" OR startLine=N, endLine=N (1-based)
- annotate: annotations=[{find: "text", text: "note", style: "info|warning|error"}] OR use line=N
- Both tools need path and version (get version from get_document_metadata first)

Show, don't tell. The user is looking at VS Code—put the information where their eyes are.
</visual-communication>

Do not attempt to edit files via these tools—edits are handled separately via the edit tool.
</vscode-integration>`
}

var (
	vscodeWatcher     *fsnotify.Watcher
	vscodeWatcherOnce sync.Once
	vscodeWatcherMu   sync.Mutex
)

// DiscoverVSCodeMCP checks for the VS Code MCP discovery file and returns
// an MCPConfig if found and valid.
func DiscoverVSCodeMCP(workingDir string) (*config.MCPConfig, error) {
	discoveryPath := filepath.Join(workingDir, vscodeMCPDiscoveryDir, vscodeMCPDiscoveryFile)

	data, err := os.ReadFile(discoveryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Not an error, just not available
		}
		return nil, fmt.Errorf("failed to read VS Code MCP discovery file: %w", err)
	}

	var discovery VSCodeMCPDiscoveryFile
	if err := json.Unmarshal(data, &discovery); err != nil {
		return nil, fmt.Errorf("failed to parse VS Code MCP discovery file: %w", err)
	}

	// Validate the PID is still alive.
	if !isPIDAlive(discovery.PID) {
		slog.Debug("VS Code MCP discovery file has stale PID", "pid", discovery.PID)
		return nil, nil
	}

	// Validate protocol.
	if discovery.Protocol != "mcp-http" {
		slog.Warn("VS Code MCP discovery file has unknown protocol", "protocol", discovery.Protocol)
		return nil, nil
	}

	url := fmt.Sprintf("http://%s:%d/mcp", discovery.Host, discovery.Port)
	slog.Info("Discovered VS Code MCP server", "url", url, "pid", discovery.PID)

	return &config.MCPConfig{
		Type: config.MCPHttp,
		URL:  url,
	}, nil
}

// isPIDAlive checks if a process with the given PID is still running.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without sending a signal.
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// StartVSCodeMCPWatcher starts a file watcher for VS Code MCP discovery changes.
// When the discovery file changes, it triggers MCP reconnection.
func StartVSCodeMCPWatcher(ctx context.Context, workingDir string, cfg *config.Config) {
	vscodeWatcherOnce.Do(func() {
		go runVSCodeWatcher(ctx, workingDir, cfg)
	})
}

func runVSCodeWatcher(ctx context.Context, workingDir string, cfg *config.Config) {
	crushDir := filepath.Join(workingDir, vscodeMCPDiscoveryDir)

	// Ensure .crush directory exists for watching.
	if err := os.MkdirAll(crushDir, 0o755); err != nil {
		slog.Warn("Failed to create .crush directory for VS Code MCP watcher", "error", err)
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("Failed to create VS Code MCP file watcher", "error", err)
		return
	}

	vscodeWatcherMu.Lock()
	vscodeWatcher = watcher
	vscodeWatcherMu.Unlock()

	defer func() {
		vscodeWatcherMu.Lock()
		if vscodeWatcher != nil {
			vscodeWatcher.Close()
			vscodeWatcher = nil
		}
		vscodeWatcherMu.Unlock()
	}()

	if err := watcher.Add(crushDir); err != nil {
		slog.Warn("Failed to watch .crush directory for VS Code MCP", "error", err)
		return
	}

	slog.Debug("Started VS Code MCP file watcher", "path", crushDir)

	// Debounce rapid file changes.
	var debounceTimer *time.Timer
	const debounceDelay = 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Only care about the vscode-mcp.json file.
			if filepath.Base(event.Name) != vscodeMCPDiscoveryFile {
				continue
			}

			// Debounce: wait for writes to settle.
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceDelay, func() {
				handleVSCodeDiscoveryChange(ctx, workingDir, cfg)
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("VS Code MCP watcher error", "error", err)
		}
	}
}

func handleVSCodeDiscoveryChange(ctx context.Context, workingDir string, cfg *config.Config) {
	mcpCfg, err := DiscoverVSCodeMCP(workingDir)
	if err != nil {
		slog.Warn("Failed to discover VS Code MCP on file change", "error", err)
		return
	}

	if mcpCfg == nil {
		// Discovery file removed or stale - disconnect if connected.
		state, exists := states.Get(vscodeMCPName)
		if exists && state.State == StateConnected {
			slog.Info("VS Code MCP disconnected")
			updateState(vscodeMCPName, StateDisabled, nil, nil, Counts{})
			if state.Client != nil {
				state.Client.Close()
			}
			sessions.Del(vscodeMCPName)
		}
		return
	}

	// Check if we need to reconnect (URL changed or not connected).
	state, exists := states.Get(vscodeMCPName)
	existingURL := ""
	if exists && cfg.MCP[vscodeMCPName].URL != "" {
		existingURL = cfg.MCP[vscodeMCPName].URL
	}

	if mcpCfg.URL != existingURL || !exists || state.State != StateConnected {
		slog.Info("VS Code MCP discovery changed, reconnecting", "url", mcpCfg.URL)

		// Close existing session if any.
		if exists && state.Client != nil {
			state.Client.Close()
			sessions.Del(vscodeMCPName)
		}

		// Update config and initialize.
		if cfg.MCP == nil {
			cfg.MCP = make(config.MCPs)
		}
		cfg.MCP[vscodeMCPName] = *mcpCfg

		// Initialize the new connection.
		initializeVSCodeMCP(ctx, cfg, *mcpCfg)
	}
}

func initializeVSCodeMCP(ctx context.Context, cfg *config.Config, mcpCfg config.MCPConfig) {
	updateState(vscodeMCPName, StateStarting, nil, nil, Counts{})

	session, err := createSession(ctx, vscodeMCPName, mcpCfg, cfg.Resolver())
	if err != nil {
		return
	}

	tools, err := getTools(ctx, session)
	if err != nil {
		slog.Error("Error listing VS Code MCP tools", "error", err)
		updateState(vscodeMCPName, StateError, err, nil, Counts{})
		session.Close()
		return
	}

	prompts, err := getPrompts(ctx, session)
	if err != nil {
		slog.Error("Error listing VS Code MCP prompts", "error", err)
		updateState(vscodeMCPName, StateError, err, nil, Counts{})
		session.Close()
		return
	}

	resources, err := getResources(ctx, session)
	if err != nil {
		slog.Error("Error listing VS Code MCP resources", "error", err)
		updateState(vscodeMCPName, StateError, err, nil, Counts{})
		session.Close()
		return
	}

	toolCount := updateTools(cfg, vscodeMCPName, tools)
	updatePrompts(vscodeMCPName, prompts)
	resourceCount := updateResources(vscodeMCPName, resources)
	sessions.Set(vscodeMCPName, session)

	updateState(vscodeMCPName, StateConnected, nil, session, Counts{
		Tools:     toolCount,
		Prompts:   len(prompts),
		Resources: resourceCount,
	})
}

// InitializeVSCodeMCPOnStartup attempts to discover and connect to VS Code MCP
// during application startup. This is called from Initialize().
func InitializeVSCodeMCPOnStartup(ctx context.Context, _ permission.Service, cfg *config.Config) {
	workingDir := cfg.WorkingDir()

	// Check if user has manually configured 'vscode' MCP - don't override.
	if _, exists := cfg.MCP[vscodeMCPName]; exists {
		slog.Debug("VS Code MCP already configured manually, skipping auto-discovery")
		return
	}

	// Try to discover VS Code MCP.
	mcpCfg, err := DiscoverVSCodeMCP(workingDir)
	if err != nil {
		slog.Debug("Failed to discover VS Code MCP", "error", err)
		return
	}

	if mcpCfg == nil {
		slog.Debug("No VS Code MCP discovery file found")
		// Start watcher to detect when VS Code starts later.
		StartVSCodeMCPWatcher(ctx, workingDir, cfg)
		return
	}

	// Add to config and initialize.
	if cfg.MCP == nil {
		cfg.MCP = make(config.MCPs)
	}
	cfg.MCP[vscodeMCPName] = *mcpCfg

	initializeVSCodeMCP(ctx, cfg, *mcpCfg)

	// Start watcher for reconnection on VS Code restart.
	StartVSCodeMCPWatcher(ctx, workingDir, cfg)
}
