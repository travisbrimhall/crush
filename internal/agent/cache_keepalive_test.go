package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheKeepalive_Touch(t *testing.T) {
	t.Parallel()

	t.Run("schedules ping after interval", func(t *testing.T) {
		t.Parallel()

		ka := NewCacheKeepalive()
		defer ka.StopAll()

		var pingCount atomic.Int32
		ping := func(ctx context.Context) error {
			pingCount.Add(1)
			return nil
		}

		// Touch with a very short interval for testing.
		// We can't easily override the interval, so just verify the timer is set.
		ka.Touch("session-1", ping)

		// Verify session is tracked.
		ka.mu.Lock()
		_, exists := ka.sessions["session-1"]
		ka.mu.Unlock()
		assert.True(t, exists, "session should be tracked")

		// Ping shouldn't have fired yet.
		assert.Equal(t, int32(0), pingCount.Load())
	})

	t.Run("resets timer on subsequent touch", func(t *testing.T) {
		t.Parallel()

		ka := NewCacheKeepalive()
		defer ka.StopAll()

		var pingCount atomic.Int32
		ping := func(ctx context.Context) error {
			pingCount.Add(1)
			return nil
		}

		ka.Touch("session-1", ping)

		// Get the first timer.
		ka.mu.Lock()
		firstTimer := ka.sessions["session-1"].timer
		ka.mu.Unlock()

		// Touch again.
		ka.Touch("session-1", ping)

		// Timer should be different (old one stopped, new one created).
		ka.mu.Lock()
		secondTimer := ka.sessions["session-1"].timer
		ka.mu.Unlock()

		assert.NotSame(t, firstTimer, secondTimer, "timer should be replaced")
	})

	t.Run("stop cancels timer", func(t *testing.T) {
		t.Parallel()

		ka := NewCacheKeepalive()

		ping := func(ctx context.Context) error {
			return nil
		}

		ka.Touch("session-1", ping)
		ka.Stop("session-1")

		ka.mu.Lock()
		_, exists := ka.sessions["session-1"]
		ka.mu.Unlock()
		assert.False(t, exists, "session should be removed")
	})

	t.Run("stopAll cancels all timers", func(t *testing.T) {
		t.Parallel()

		ka := NewCacheKeepalive()

		ping := func(ctx context.Context) error {
			return nil
		}

		ka.Touch("session-1", ping)
		ka.Touch("session-2", ping)
		ka.Touch("session-3", ping)

		ka.StopAll()

		ka.mu.Lock()
		count := len(ka.sessions)
		ka.mu.Unlock()
		assert.Equal(t, 0, count, "all sessions should be removed")
	})

	t.Run("multiple sessions independent", func(t *testing.T) {
		t.Parallel()

		ka := NewCacheKeepalive()
		defer ka.StopAll()

		ping1 := func(ctx context.Context) error { return nil }
		ping2 := func(ctx context.Context) error { return nil }

		ka.Touch("session-1", ping1)
		ka.Touch("session-2", ping2)

		ka.mu.Lock()
		count := len(ka.sessions)
		ka.mu.Unlock()
		assert.Equal(t, 2, count, "both sessions should be tracked")

		ka.Stop("session-1")

		ka.mu.Lock()
		_, exists1 := ka.sessions["session-1"]
		_, exists2 := ka.sessions["session-2"]
		ka.mu.Unlock()
		assert.False(t, exists1, "session-1 should be removed")
		assert.True(t, exists2, "session-2 should still exist")
	})
}

func TestCacheKeepalive_PingReschedules(t *testing.T) {
	t.Parallel()

	// This test verifies that after a successful ping, the timer is rescheduled.
	// We use a custom short-interval approach by directly calling doPing.

	ka := NewCacheKeepalive()
	defer ka.StopAll()

	var pingCount atomic.Int32
	ping := func(ctx context.Context) error {
		pingCount.Add(1)
		return nil
	}

	// Manually trigger doPing.
	ctx := context.Background()
	ka.doPing(ctx, "session-1", ping)

	// Ping should have fired once.
	require.Equal(t, int32(1), pingCount.Load())

	// Timer should be rescheduled (session should exist).
	ka.mu.Lock()
	_, exists := ka.sessions["session-1"]
	ka.mu.Unlock()
	assert.True(t, exists, "session should be rescheduled after ping")
}

func TestCacheKeepalive_PingCancellation(t *testing.T) {
	t.Parallel()

	ka := NewCacheKeepalive()

	pingCalled := make(chan struct{})
	ping := func(ctx context.Context) error {
		close(pingCalled)
		return nil
	}

	ka.Touch("session-1", ping)

	// Stop should cancel the ping context.
	ka.Stop("session-1")

	// Session should be removed.
	ka.mu.Lock()
	_, exists := ka.sessions["session-1"]
	ka.mu.Unlock()
	assert.False(t, exists, "session should be removed after Stop")
}
