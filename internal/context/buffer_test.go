package context

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// makePayload creates a valid context payload for testing.
func makePayload(t *testing.T, workspaceID, filePath string, diagCode string, line int) []byte {
	t.Helper()
	payload := map[string]any{
		"schemaVersion": SchemaVersion,
		"event":         EventDiagnostic,
		"source":        SourceVSCode,
		"workspace": map[string]any{
			"id":   workspaceID,
			"root": "/test/workspace",
			"name": "test",
		},
		"file": map[string]any{
			"path":    filePath,
			"version": 1,
		},
		"diagnostic": map[string]any{
			"code":    diagCode,
			"message": fmt.Sprintf("Error %s on line %d", diagCode, line),
			"range": map[string]any{
				"start": map[string]any{"line": line, "character": 0},
				"end":   map[string]any{"line": line, "character": 10},
			},
		},
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}

func TestDuplicateStorm(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// POST same diagnostic 100 times.
	payload := makePayload(t, "ws-123", "src/foo.ts", "TS2339", 42)

	for i := 0; i < 100; i++ {
		result, err := session.AddContext(payload)
		require.NoError(t, err)

		if i == 0 {
			require.True(t, result.IsNew)
			require.Equal(t, 1, result.Count)
		} else {
			require.False(t, result.IsNew)
			require.Equal(t, i+1, result.Count)
		}
	}

	// Should have exactly 1 context.
	require.Equal(t, 1, session.ContextCount())

	// Count should be 100.
	contexts := session.Contexts()
	require.Len(t, contexts, 1)
	require.Equal(t, 100, contexts[0].Count)
}

func TestOverflowEviction(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// POST 60 unique contexts.
	for i := 0; i < 60; i++ {
		payload := makePayload(t, "ws-123", fmt.Sprintf("src/file%d.ts", i), "TS2339", i)
		result, err := session.AddContext(payload)
		require.NoError(t, err)
		require.True(t, result.IsNew)
	}

	// Should have exactly 50 (MaxContexts).
	require.Equal(t, MaxContexts, session.ContextCount())

	// The oldest 10 should be evicted (files 0-9).
	contexts := session.Contexts()
	for _, ctx := range contexts {
		// Newest first, so we should see files 59, 58, ..., 10.
		require.NotContains(t, ctx.FilePath, "file0.ts")
		require.NotContains(t, ctx.FilePath, "file9.ts")
	}
}

func TestMemoryLimitEviction(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// Create a large payload (~500KB each).
	largeData := strings.Repeat("x", 500*1024)

	for i := 0; i < 50; i++ {
		payload := map[string]any{
			"schemaVersion": SchemaVersion,
			"event":         EventDiagnostic,
			"source":        SourceVSCode,
			"workspace": map[string]any{
				"id":   "ws-123",
				"root": "/test",
				"name": "test",
			},
			"file": map[string]any{
				"path":    fmt.Sprintf("src/file%d.ts", i),
				"version": 1,
			},
			"diagnostic": map[string]any{
				"code":    "TS2339",
				"message": fmt.Sprintf("Error %d", i),
				"data":    largeData,
			},
		}
		data, err := json.Marshal(payload)
		require.NoError(t, err)

		_, err = session.AddContext(data)
		require.NoError(t, err)
	}

	// Should be under 20MB limit.
	require.LessOrEqual(t, session.TotalBytes(), int64(MaxTotalBytes))
}

func TestConcurrentSubmit(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// Add a context first.
	payload := makePayload(t, "ws-123", "src/foo.ts", "TS2339", 42)
	_, err := session.AddContext(payload)
	require.NoError(t, err)

	// Simulate concurrent submit attempts.
	var wg sync.WaitGroup
	results := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- session.StartRun()
		}()
	}

	wg.Wait()
	close(results)

	// Count successes and failures.
	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrAgentRunning)
			failures++
		}
	}

	// Exactly one should succeed.
	require.Equal(t, 1, successes)
	require.Equal(t, 9, failures)
}

func TestCrossWorkspaceRejection(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// First context binds workspace.
	payload1 := makePayload(t, "ws-123", "src/foo.ts", "TS2339", 42)
	_, err := session.AddContext(payload1)
	require.NoError(t, err)
	require.Equal(t, "ws-123", session.WorkspaceID())

	// Second context from different workspace should fail.
	payload2 := makePayload(t, "ws-456", "src/bar.ts", "TS2339", 42)
	_, err = session.AddContext(payload2)
	require.ErrorIs(t, err, ErrWorkspaceMismatch)

	// Buffer should still have only 1 context.
	require.Equal(t, 1, session.ContextCount())
}

func TestPayloadTooLarge(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// Create payload over 1MB.
	largeData := strings.Repeat("x", MaxPayloadBytes+1)
	payload := []byte(fmt.Sprintf(`{"schemaVersion":"%s","event":"diagnostic_fix_request","source":"vscode","workspace":{"id":"ws-123"},"data":"%s"}`, SchemaVersion, largeData))

	_, err := session.AddContext(payload)
	require.ErrorIs(t, err, ErrPayloadTooLarge)
}

func TestInvalidSchemaVersion(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	payload := []byte(`{"schemaVersion":"0.9","event":"diagnostic_fix_request","source":"vscode","workspace":{"id":"ws-123"}}`)
	_, err := session.AddContext(payload)
	require.ErrorIs(t, err, ErrInvalidSchema)
}

func TestServerCSRFProtection(t *testing.T) {
	t.Parallel()
	server := NewServer("127.0.0.1:9119")
	handler := server.Handler()

	// POST without source or token should fail.
	payload := makePayload(t, "ws-123", "src/foo.ts", "TS2339", 42)
	req := httptest.NewRequest("POST", "/context", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// POST with wrong token should fail.
	req = httptest.NewRequest("POST", "/context", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crush-Token", "wrong-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// POST with correct token should succeed.
	req = httptest.NewRequest("POST", "/context", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crush-Token", server.Token())
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestServerTrustedSourceExemption(t *testing.T) {
	t.Parallel()
	server := NewServer("127.0.0.1:9119")
	handler := server.Handler()

	payload := makePayload(t, "ws-456", "src/bar.ts", "TS2345", 10)

	// POST with X-Crush-Source: vscode should succeed without token.
	req := httptest.NewRequest("POST", "/context", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crush-Source", "vscode")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// POST with X-Crush-Source: docker should also succeed without token.
	payload2 := makePayload(t, "ws-456", "src/baz.ts", "TS2345", 20)
	req = httptest.NewRequest("POST", "/context", strings.NewReader(string(payload2)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crush-Source", "docker")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// POST with X-Crush-Source: chrome (untrusted) should fail without token.
	payload3 := makePayload(t, "ws-456", "src/qux.ts", "TS2345", 30)
	req = httptest.NewRequest("POST", "/context", strings.NewReader(string(payload3)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crush-Source", "chrome")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestServerStatus(t *testing.T) {
	t.Parallel()
	server := NewServer("127.0.0.1:9119")
	handler := server.Handler()

	// Add some contexts (use trusted source header).
	for i := 0; i < 3; i++ {
		payload := makePayload(t, "ws-123", fmt.Sprintf("src/file%d.ts", i), "TS2339", i)
		req := httptest.NewRequest("POST", "/context", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Crush-Source", "vscode")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	// GET /status (no auth required).
	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status StatusResponse
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)

	require.Equal(t, "ws-123", status.WorkspaceID)
	require.Equal(t, 3, status.ContextCount)
	require.Len(t, status.Contexts, 3)
}

func TestServerConcurrentRun(t *testing.T) {
	t.Parallel()
	server := NewServer("127.0.0.1:9119")
	handler := server.Handler()

	// Add a context (use trusted source header).
	payload := makePayload(t, "ws-123", "src/foo.ts", "TS2339", 42)
	req := httptest.NewRequest("POST", "/context", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crush-Source", "vscode")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// First submit should succeed (trusted source).
	req = httptest.NewRequest("POST", "/submit", nil)
	req.Header.Set("X-Crush-Source", "vscode")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Second submit should fail with 409.
	req = httptest.NewRequest("POST", "/submit", nil)
	req.Header.Set("X-Crush-Source", "vscode")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	body, _ := io.ReadAll(rec.Body)
	require.Contains(t, string(body), "already running")
}

func TestHashDifferentErrors(t *testing.T) {
	t.Parallel()
	session := NewSession("test-token")

	// Two different TypeScript errors on the same line should NOT dedupe.
	payload1 := makePayload(t, "ws-123", "src/foo.ts", "TS2339", 42)
	payload2 := makePayload(t, "ws-123", "src/foo.ts", "TS2322", 42) // Different error code

	result1, err := session.AddContext(payload1)
	require.NoError(t, err)
	require.True(t, result1.IsNew)

	result2, err := session.AddContext(payload2)
	require.NoError(t, err)
	require.True(t, result2.IsNew) // Should be new, not deduped

	require.Equal(t, 2, session.ContextCount())
}
