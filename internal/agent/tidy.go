package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"text/template"
	"time"

	"charm.land/fantasy"
)

//go:embed templates/tidy.md
var tidyPromptTmpl []byte

var tidyTemplate = template.Must(template.New("tidy").Parse(string(tidyPromptTmpl)))

const (
	// tidyMinContentSize is the minimum content size to consider for tidying.
	tidyMinContentSize = 500
	// tidyMinMessageAge is how many messages old content must be to tidy.
	tidyMinMessageAge = 0
	// tidyIdleInterval is how long to wait after last activity before tidying.
	tidyIdleInterval = 30 * time.Second
)

// TidyManager manages background context tidying for sessions.
// It runs a subagent during idle periods to compress bulky tool outputs.
type TidyManager struct {
	mu       sync.Mutex
	sessions map[string]*tidySession
}

type tidySession struct {
	timer        *time.Timer
	cancel       context.CancelFunc
	compressions map[string]string // tool_call_id -> compressed content
}

// tidyCompression represents a single compression from the tidy agent.
type tidyCompression struct {
	ToolCallID string `json:"tool_call_id"`
	Summary    string `json:"summary"`
}

// NewTidyManager creates a new tidy manager.
func NewTidyManager() *TidyManager {
	return &TidyManager{
		sessions: make(map[string]*tidySession),
	}
}

// Touch resets the idle timer for a session. Call after every user/assistant
// turn to delay tidying until the conversation is idle.
func (t *TidyManager) Touch(sessionID string, runTidy func(context.Context) (map[string]string, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Cancel existing timer.
	if sess, ok := t.sessions[sessionID]; ok {
		sess.timer.Stop()
		if sess.cancel != nil {
			sess.cancel()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(tidyIdleInterval, func() {
		t.doTidy(ctx, sessionID, runTidy)
	})

	if existing, ok := t.sessions[sessionID]; ok {
		existing.timer = timer
		existing.cancel = cancel
	} else {
		t.sessions[sessionID] = &tidySession{
			timer:        timer,
			cancel:       cancel,
			compressions: make(map[string]string),
		}
	}
}

// GetCompression returns the compressed content for a tool call, if available.
func (t *TidyManager) GetCompression(sessionID, toolCallID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if sess, ok := t.sessions[sessionID]; ok {
		content, found := sess.compressions[toolCallID]
		return content, found
	}
	return "", false
}

// Stop cancels tidying for a session.
func (t *TidyManager) Stop(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if sess, ok := t.sessions[sessionID]; ok {
		sess.timer.Stop()
		if sess.cancel != nil {
			sess.cancel()
		}
		delete(t.sessions, sessionID)
	}
}

// RunNow immediately runs tidy for a session, bypassing the idle timer.
func (t *TidyManager) RunNow(ctx context.Context, sessionID string, runTidy func(context.Context) (map[string]string, error)) {
	t.mu.Lock()
	// Cancel any pending timer.
	if sess, ok := t.sessions[sessionID]; ok {
		sess.timer.Stop()
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	t.mu.Unlock()

	t.doTidy(ctx, sessionID, runTidy)
}

func (t *TidyManager) doTidy(ctx context.Context, sessionID string, runTidy func(context.Context) (map[string]string, error)) {
	slog.Debug("Running tidy", "session", sessionID)

	compressions, err := runTidy(ctx)
	if err != nil {
		slog.Debug("Tidy failed", "session", sessionID, "error", err)
		return
	}

	if len(compressions) == 0 {
		return
	}

	t.mu.Lock()
	if sess, ok := t.sessions[sessionID]; ok {
		for id, content := range compressions {
			sess.compressions[id] = content
		}
	}
	t.mu.Unlock()

	slog.Debug("Tidy completed", "session", sessionID, "compressions", len(compressions))
}

// tidyCandidate represents a tool output that might be worth compressing.
type tidyCandidate struct {
	ToolCallID string
	ToolName   string
	Content    string
	MsgIndex   int
}

// FindTidyCandidates finds tool outputs that are old and bulky enough to tidy.
func FindTidyCandidates(messages []fantasy.Message) []tidyCandidate {
	var candidates []tidyCandidate

	totalMessages := len(messages)

	for i, msg := range messages {
		// Must be old enough.
		if totalMessages-i < tidyMinMessageAge {
			break
		}

		if msg.Role != fantasy.MessageRoleTool {
			continue
		}

		for _, part := range msg.Content {
			toolResult, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}

			text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](toolResult.Output)
			if !ok {
				continue
			}

			if len(text.Text) < tidyMinContentSize {
				continue
			}

			candidates = append(candidates, tidyCandidate{
				ToolCallID: toolResult.ToolCallID,
				Content:    text.Text,
				MsgIndex:   i,
			})
		}
	}

	return candidates
}

// BuildTidyPrompt builds the prompt for the tidy subagent.
func BuildTidyPrompt(candidates []tidyCandidate) (string, error) {
	var toolOutputs strings.Builder
	for _, c := range candidates {
		fmt.Fprintf(&toolOutputs, "--- Tool Call ID: %s ---\n", c.ToolCallID)
		// Include preview to limit prompt size.
		content := c.Content
		if len(content) > 2000 {
			content = content[:2000] + "\n... (truncated, " + fmt.Sprintf("%d", len(c.Content)) + " chars total)"
		}
		toolOutputs.WriteString(content)
		toolOutputs.WriteString("\n\n")
	}

	var buf bytes.Buffer
	err := tidyTemplate.Execute(&buf, map[string]string{
		"ToolOutputs": toolOutputs.String(),
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// ParseTidyResponse parses the tidy agent's JSON response.
func ParseTidyResponse(response string) ([]tidyCompression, error) {
	// Find JSON array in response.
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found")
	}

	var compressions []tidyCompression
	if err := json.Unmarshal([]byte(response[start:end+1]), &compressions); err != nil {
		return nil, err
	}

	return compressions, nil
}

// ApplyTidyCompressions applies cached compressions to messages.
// Messages are modified in place. Returns count of compressions applied.
func ApplyTidyCompressions(messages []fantasy.Message, getCompression func(toolCallID string) (string, bool)) int {
	count := 0

	for i := range messages {
		msg := &messages[i]
		if msg.Role != fantasy.MessageRoleTool {
			continue
		}

		for j, part := range msg.Content {
			toolResult, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}

			compressed, found := getCompression(toolResult.ToolCallID)
			if !found {
				continue
			}

			msg.Content[j] = fantasy.ToolResultPart{
				ToolCallID:      toolResult.ToolCallID,
				Output:          fantasy.ToolResultOutputContentText{Text: compressed},
				ProviderOptions: toolResult.ProviderOptions,
			}
			count++
		}
	}

	return count
}
