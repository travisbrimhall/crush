package context

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
)

// Server is the local API server for external context ingestion.
type Server struct {
	session *Session
	broker  *pubsub.Broker[*Entry]
	mu      sync.RWMutex
	addr    string
}

// NewServer creates a new context server bound to the given address.
func NewServer(addr string) *Server {
	token := generateToken()
	return &Server{
		session: NewSession(token),
		broker:  pubsub.NewBroker[*Entry](),
		addr:    addr,
	}
}

// Token returns the CSRF session token. Browser-based extensions must include
// this in the X-Crush-Token header. Trusted sources (vscode, docker) are exempt.
func (s *Server) Token() string {
	return s.session.Token()
}

// Addr returns the server's listen address.
func (s *Server) Addr() string {
	return s.addr
}

// Session returns the underlying session for direct access.
func (s *Server) Session() *Session {
	return s.session
}

// Subscribe returns a channel that receives context entries as they arrive.
func (s *Server) Subscribe(ctx context.Context) <-chan pubsub.Event[*Entry] {
	return s.broker.Subscribe(ctx)
}

// Handler returns an http.Handler for the context API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /context", s.handleContext)
	mux.HandleFunc("POST /submit", s.handleSubmit)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /sessions", s.handleSessions)
	return s.withAuth(mux)
}

// trustedSources are sources that run in isolated contexts with no CSRF risk.
// These don't require token authentication.
var trustedSources = map[string]bool{
	"vscode": true,
	"docker": true,
}

// withAuth wraps the handler with CSRF token validation for mutating endpoints.
// Trusted sources (vscode, docker) are exempt; browser sources require a token.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET requests don't need auth.
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}

		// Trusted sources (vscode, docker) don't need token.
		source := r.Header.Get("X-Crush-Source")
		if trustedSources[source] {
			next.ServeHTTP(w, r)
			return
		}

		// Untrusted sources (browser, unknown) require valid token.
		token := r.Header.Get("X-Crush-Token")
		if token != s.session.Token() {
			writeError(w, http.StatusUnauthorized, ErrInvalidToken)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleContext handles POST /context.
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	// Read body with size limit.
	r.Body = http.MaxBytesReader(w, r.Body, MaxPayloadBytes+1024) // Small buffer for headers

	var payload json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	result, err := s.session.AddContext(payload)
	if err != nil {
		switch err {
		case ErrWorkspaceMismatch:
			writeError(w, http.StatusConflict, err)
		case ErrPayloadTooLarge:
			writeError(w, http.StatusRequestEntityTooLarge, err)
		case ErrInvalidSchema:
			writeError(w, http.StatusBadRequest, err)
		default:
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}

	// Publish to subscribers (TUI).
	if result.Entry != nil {
		s.broker.Publish(pubsub.CreatedEvent, result.Entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "accepted",
		"contextId": result.ContextID,
		"isNew":     result.IsNew,
		"count":     result.Count,
	})
}

// handleSubmit handles POST /submit.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	// Try to start a run.
	if err := s.session.StartRun(); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}

	// In real implementation, this would trigger the agent.
	// For now, just acknowledge and let caller handle the run.
	contexts := s.session.Contexts()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "running",
		"contextCount": len(contexts),
	})
}

// StatusResponse is returned by GET /status.
type StatusResponse struct {
	WorkspaceID  string         `json:"workspaceId"`
	ContextCount int            `json:"contextCount"`
	TotalBytes   int64          `json:"totalBytes"`
	IsRunning    bool           `json:"isRunning"`
	Contexts     []ContextBrief `json:"contexts"`
}

// ContextBrief is a summary of a context for status display.
type ContextBrief struct {
	ID        string    `json:"id"`
	Source    Source    `json:"source"`
	EventType EventType `json:"eventType"`
	FilePath  string    `json:"filePath,omitempty"`
	Count     int       `json:"count"`
	Age       string    `json:"age"` // Human-readable age
}

// handleStatus handles GET /status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	contexts := s.session.Contexts()
	briefs := make([]ContextBrief, len(contexts))

	for i, ctx := range contexts {
		briefs[i] = ContextBrief{
			ID:        ctx.ID,
			Source:    ctx.Source,
			EventType: ctx.EventType,
			FilePath:  ctx.FilePath,
			Count:     ctx.Count,
			Age:       formatAge(ctx.ReceivedAt),
		}
	}

	writeJSON(w, http.StatusOK, StatusResponse{
		WorkspaceID:  s.session.WorkspaceID(),
		ContextCount: s.session.ContextCount(),
		TotalBytes:   s.session.TotalBytes(),
		IsRunning:    s.session.IsRunning(),
		Contexts:     briefs,
	})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes an error response.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"status":  "error",
		"code":    status,
		"message": err.Error(),
	})
}

// generateToken creates a random 128-bit session token.
func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// formatAge returns a human-readable age string.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// SessionInfo is returned in the sessions list for the extension picker.
type SessionInfo struct {
	Port       int    `json:"port"`
	Token      string `json:"token"`
	WorkingDir string `json:"workingDir"`
	Model      string `json:"model,omitempty"`
	StartedAt  string `json:"startedAt"`
}

// handleSessions returns all registered Crush sessions for the extension picker.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	reg, err := ReadRegistry()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	sessions := make([]SessionInfo, len(reg.Sessions))
	for i, entry := range reg.Sessions {
		sessions[i] = SessionInfo{
			Port:       entry.Port,
			Token:      entry.Token,
			WorkingDir: entry.WorkingDir,
			Model:      entry.Model,
			StartedAt:  formatAge(entry.StartedAt),
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
	})
}
