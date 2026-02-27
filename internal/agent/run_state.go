package agent

import (
	"context"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// RunState holds mutable state for a single Run() invocation.
// All fields are owned by the current goroutine during streaming.
// This replaces closure-captured variables for explicit state management.
//
// RunState is single-use per Run() invocation. Do not reuse across calls.
type RunState struct {
	// Immutable after creation.
	Call         SessionAgentCall
	Session      session.Session
	Model        Model
	SystemPrompt string
	PromptPrefix string
	Tools        []fantasy.AgentTool
	HasSummary   bool
	StartTime    time.Time
	History      []fantasy.Message
	Files        []fantasy.FilePart

	// ParentCtx is never canceled by streaming; used for DB writes after
	// cancellation. Do NOT use this for streaming operations.
	ParentCtx context.Context

	// Mutated during streaming.
	CurrentAssistant *message.Message
	ShouldSummarize  bool
	LSPBatcher       *lsp.Batcher

	// SessionLock guards session updates during OnStepFinish.
	SessionLock sync.Mutex
}

// RunStateOptions contains dependencies for creating RunState.
type RunStateOptions struct {
	Sessions   session.Service
	Messages   message.Service
	LSPManager *lsp.Manager
}

// NewRunState creates state from a SessionAgentCall.
// It fetches the session, prepares message history, and snapshots immutable
// config. Returns error if session fetch fails.
func NewRunState(
	ctx context.Context,
	call SessionAgentCall,
	model Model,
	systemPrompt string,
	promptPrefix string,
	tools []fantasy.AgentTool,
	opts RunStateOptions,
) (*RunState, error) {
	currentSession, err := opts.Sessions.Get(ctx, call.SessionID)
	if err != nil {
		return nil, err
	}

	return &RunState{
		Call:         call,
		Session:      currentSession,
		Model:        model,
		SystemPrompt: systemPrompt,
		PromptPrefix: promptPrefix,
		Tools:        tools,
		HasSummary:   currentSession.SummaryMessageID != "",
		StartTime:    time.Now(),
		ParentCtx:    ctx,
	}, nil
}

// IsFirstMessage returns true if this is the first message in the session.
func (s *RunState) IsFirstMessage() bool {
	return len(s.History) == 0
}

// UpdateSession safely updates the session under lock.
func (s *RunState) UpdateSession(sess session.Session) {
	s.SessionLock.Lock()
	defer s.SessionLock.Unlock()
	s.Session = sess
}

// GetSession safely retrieves the current session under lock.
func (s *RunState) GetSession() session.Session {
	s.SessionLock.Lock()
	defer s.SessionLock.Unlock()
	return s.Session
}

// Finish cleans up resources. Should be called via defer after creation.
func (s *RunState) Finish(ctx context.Context) {
	if s.LSPBatcher != nil {
		s.LSPBatcher.Flush(ctx)
		s.LSPBatcher = nil
	}
}
