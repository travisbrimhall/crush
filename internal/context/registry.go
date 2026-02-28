package context

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// RegistryEntry represents a single Crush session in the global registry.
type RegistryEntry struct {
	Port        int       `json:"port"`
	Token       string    `json:"token"`
	PID         int       `json:"pid"`
	WorkingDir  string    `json:"working_dir"`
	WorkspaceID string    `json:"workspace_id"`
	Model       string    `json:"model,omitempty"`
	StartedAt   time.Time `json:"started_at"`
}

// Registry holds all known Crush sessions.
type Registry struct {
	Sessions []RegistryEntry `json:"sessions"`
}

// RegistryPath returns the path to the global registry file.
func RegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".crush", "sessions.json"), nil
}

// ReadRegistry reads the global registry, cleaning up stale entries.
func ReadRegistry() (*Registry, error) {
	path, err := RegistryPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{Sessions: []RegistryEntry{}}, nil
		}
		return nil, err
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		// Corrupted file, start fresh.
		return &Registry{Sessions: []RegistryEntry{}}, nil
	}

	// Filter out dead PIDs.
	alive := make([]RegistryEntry, 0, len(reg.Sessions))
	for _, entry := range reg.Sessions {
		if isProcessAlive(entry.PID) {
			alive = append(alive, entry)
		}
	}
	reg.Sessions = alive

	return &reg, nil
}

// writeRegistry writes the registry to disk.
func writeRegistry(reg *Registry) error {
	path, err := RegistryPath()
	if err != nil {
		return err
	}

	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// Register adds a session to the global registry.
func Register(entry RegistryEntry) error {
	reg, err := ReadRegistry()
	if err != nil {
		return err
	}

	// Remove any existing entry for this PID (re-registration).
	filtered := make([]RegistryEntry, 0, len(reg.Sessions))
	for _, e := range reg.Sessions {
		if e.PID != entry.PID {
			filtered = append(filtered, e)
		}
	}

	filtered = append(filtered, entry)
	reg.Sessions = filtered

	return writeRegistry(reg)
}

// Unregister removes a session from the global registry by PID.
func Unregister(pid int) error {
	reg, err := ReadRegistry()
	if err != nil {
		return err
	}

	filtered := make([]RegistryEntry, 0, len(reg.Sessions))
	for _, e := range reg.Sessions {
		if e.PID != pid {
			filtered = append(filtered, e)
		}
	}
	reg.Sessions = filtered

	return writeRegistry(reg)
}

// isProcessAlive checks if a process with the given PID is still running.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, FindProcess always succeeds. Send signal 0 to check if alive.
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// LocalServerFile is the structure written to .crush/server.json.
type LocalServerFile struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// WriteLocalServerFile writes server info to .crush/server.json in the project.
func WriteLocalServerFile(workingDir string, entry RegistryEntry) error {
	crushDir := filepath.Join(workingDir, ".crush")
	if err := os.MkdirAll(crushDir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(LocalServerFile{
		Port:  entry.Port,
		Token: entry.Token,
		PID:   entry.PID,
	}, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(crushDir, "server.json"), data, 0o644)
}

// RemoveLocalServerFile removes .crush/server.json from the project.
func RemoveLocalServerFile(workingDir string) {
	_ = os.Remove(filepath.Join(workingDir, ".crush", "server.json"))
}
