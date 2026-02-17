package lsp

import (
	"context"
	"sync"
	"time"
)

// Batcher collects file paths during tool execution and batches LSP
// notifications. This avoids waiting for diagnostics on each file view,
// instead waiting once after all files are registered.
type Batcher struct {
	manager *Manager
	files   map[string]struct{}
	mu      sync.Mutex
}

// NewBatcher creates a new LSP notification batcher.
func NewBatcher(manager *Manager) *Batcher {
	return &Batcher{
		manager: manager,
		files:   make(map[string]struct{}),
	}
}

// Register adds a file path to be notified. This does not block.
func (b *Batcher) Register(filepath string) {
	if filepath == "" {
		return
	}
	b.mu.Lock()
	b.files[filepath] = struct{}{}
	b.mu.Unlock()
}

// Flush notifies all registered files and waits for diagnostics once.
// This should be called after a batch of tool executions completes.
func (b *Batcher) Flush(ctx context.Context) {
	b.mu.Lock()
	files := make([]string, 0, len(b.files))
	for f := range b.files {
		files = append(files, f)
	}
	// Clear the set for potential reuse.
	b.files = make(map[string]struct{})
	b.mu.Unlock()

	if len(files) == 0 || b.manager == nil {
		return
	}

	// Notify all files without waiting.
	for _, filepath := range files {
		b.manager.Start(ctx, filepath)
		for client := range b.manager.Clients().Seq() {
			if !client.HandlesFile(filepath) {
				continue
			}
			_ = client.OpenFileOnDemand(ctx, filepath)
			_ = client.NotifyChange(ctx, filepath)
		}
	}

	// Wait once for diagnostics across all clients.
	b.waitForDiagnostics(ctx, 5*time.Second)
}

// waitForDiagnostics waits until any client's diagnostics change or timeout.
func (b *Batcher) waitForDiagnostics(ctx context.Context, d time.Duration) {
	if b.manager.Clients().Len() == 0 {
		return
	}

	// Collect initial versions from all clients.
	type clientVersion struct {
		client  *Client
		version uint64
	}
	var clients []clientVersion
	for client := range b.manager.Clients().Seq() {
		clients = append(clients, clientVersion{
			client:  client,
			version: client.diagnostics.Version(),
		})
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(d)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			return
		case <-ticker.C:
			// Check if any client has new diagnostics.
			for _, cv := range clients {
				if cv.client.diagnostics.Version() != cv.version {
					return
				}
			}
		}
	}
}

// HasFiles returns true if there are files registered for notification.
func (b *Batcher) HasFiles() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.files) > 0
}
