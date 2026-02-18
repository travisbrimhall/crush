package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"charm.land/fantasy"
)

const (
	// cacheKeepaliveInterval is slightly less than Anthropic's 5-minute TTL.
	cacheKeepaliveInterval = 4*time.Minute + 30*time.Second
)

// CacheKeepalive manages background pings to keep Anthropic prompt caches warm.
// It tracks the last API call time per session and sends minimal requests to
// refresh the cache TTL before it expires.
type CacheKeepalive struct {
	mu       sync.Mutex
	sessions map[string]*keepaliveSession
}

type keepaliveSession struct {
	timer      *time.Timer
	cancelPing context.CancelFunc
}

// NewCacheKeepalive creates a new cache keepalive manager.
func NewCacheKeepalive() *CacheKeepalive {
	return &CacheKeepalive{
		sessions: make(map[string]*keepaliveSession),
	}
}

// Touch resets the keepalive timer for a session. Call this after every
// successful API request to Anthropic.
func (c *CacheKeepalive) Touch(sessionID string, ping func(context.Context) error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cancel any existing timer/ping for this session.
	if sess, ok := c.sessions[sessionID]; ok {
		sess.timer.Stop()
		if sess.cancelPing != nil {
			sess.cancelPing()
		}
	}

	// Create new timer.
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(cacheKeepaliveInterval, func() {
		c.doPing(ctx, sessionID, ping)
	})

	c.sessions[sessionID] = &keepaliveSession{
		timer:      timer,
		cancelPing: cancel,
	}
}

// Stop cancels the keepalive timer for a session.
func (c *CacheKeepalive) Stop(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if sess, ok := c.sessions[sessionID]; ok {
		sess.timer.Stop()
		if sess.cancelPing != nil {
			sess.cancelPing()
		}
		delete(c.sessions, sessionID)
	}
}

// StopAll cancels all keepalive timers.
func (c *CacheKeepalive) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, sess := range c.sessions {
		sess.timer.Stop()
		if sess.cancelPing != nil {
			sess.cancelPing()
		}
	}
	c.sessions = make(map[string]*keepaliveSession)
}

func (c *CacheKeepalive) doPing(ctx context.Context, sessionID string, ping func(context.Context) error) {
	slog.Debug("Cache keepalive ping", "session", sessionID)

	if err := ping(ctx); err != nil {
		// Don't retry on error - if the ping fails, the cache will expire.
		// This is fine; we'll rebuild it on the next real request.
		slog.Debug("Cache keepalive ping failed", "session", sessionID, "error", err)
		return
	}

	// Ping succeeded, schedule the next one.
	c.Touch(sessionID, ping)
}

// buildKeepalivePing creates the minimal ping function for a session.
// It sends the cached prefix with a trivial prompt to refresh the TTL.
func buildKeepalivePing(
	model fantasy.LanguageModel,
	preparedMessages []fantasy.Message,
	cacheOpts fantasy.ProviderOptions,
) func(context.Context) error {
	// Copy messages to avoid mutation issues.
	messages := make([]fantasy.Message, len(preparedMessages))
	copy(messages, preparedMessages)

	return func(ctx context.Context) error {
		// Build minimal request with the same cacheable prefix.
		// Add a trivial user message to the existing prepared messages.
		pingMessages := make([]fantasy.Message, 0, len(messages)+1)
		pingMessages = append(pingMessages, messages...)
		pingMessages = append(pingMessages, fantasy.NewUserMessage("."))

		// Apply the same cache markers.
		applyCacheMarkers(pingMessages, false, cacheOpts)

		// Send with max_tokens=1 to minimize cost.
		maxTokens := int64(1)
		resp, err := model.Stream(ctx, fantasy.Call{
			Prompt:          fantasy.Prompt(pingMessages),
			MaxOutputTokens: &maxTokens,
		})
		if err != nil {
			return err
		}

		// Consume the stream to complete the request.
		for range resp {
			// Discard output.
		}

		return nil
	}
}
