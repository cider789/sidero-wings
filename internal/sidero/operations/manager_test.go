package operations

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagerCompletesAndPublishesProgress(t *testing.T) {
	var mu sync.Mutex
	var events []Event
	m := NewManager(Config{MaximumConcurrentGlobal: 1, MaximumConcurrentPerServer: 1, Retention: time.Minute, QueueSize: 2}, func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	defer m.Close()

	op, err := m.Submit("server-a", "file_search", "key-1", func(ctx context.Context, update UpdateFunc) (any, *SafeError) {
		update(0.5, "Searching files.")
		return map[string]any{"matches": 2}, nil
	})
	require.NoError(t, err)
	got := waitTerminal(t, m, "server-a", op.ID)
	require.Equal(t, StateCompleted, got.State)
	require.Equal(t, 1.0, got.Progress)
	require.Equal(t, map[string]any{"matches": 2}, got.Result)
	mu.Lock()
	require.NotEmpty(t, events)
	require.Equal(t, "server-a", events[0].ServerID)
	var completed Event
	for _, event := range events {
		if event.Name == "sidero operation completed" {
			completed = event
		}
	}
	require.Equal(t, map[string]any{"available": true}, completed.Payload["result"])
	require.NotContains(t, completed.Payload, "matches")
	mu.Unlock()
}

func TestManagerScopesOperationsAndCoalescesIdempotencyKey(t *testing.T) {
	m := NewManager(Config{MaximumConcurrentGlobal: 1, MaximumConcurrentPerServer: 1, Retention: time.Minute, QueueSize: 2}, nil)
	defer m.Close()
	block := make(chan struct{})
	runner := func(ctx context.Context, update UpdateFunc) (any, *SafeError) {
		select {
		case <-block:
			return nil, nil
		case <-ctx.Done():
			return nil, &SafeError{Code: "operation_cancelled", Message: "The operation was cancelled."}
		}
	}
	op, err := m.Submit("server-a", "download", "same", runner)
	require.NoError(t, err)
	duplicate, err := m.Submit("server-a", "download", "same", runner)
	require.NoError(t, err)
	require.Equal(t, op.ID, duplicate.ID)
	_, ok := m.Get("server-b", op.ID)
	require.False(t, ok)
	require.True(t, m.Cancel("server-a", op.ID))
	got := waitTerminal(t, m, "server-a", op.ID)
	require.Equal(t, StateCancelled, got.State)
	close(block)
}

func TestManagerEnforcesQueueLimit(t *testing.T) {
	m := NewManager(Config{MaximumConcurrentGlobal: 1, MaximumConcurrentPerServer: 1, Retention: time.Minute, QueueSize: 1}, nil)
	defer m.Close()
	block := make(chan struct{})
	runner := func(context.Context, UpdateFunc) (any, *SafeError) { <-block; return nil, nil }
	_, err := m.Submit("a", "one", "", runner)
	require.NoError(t, err)
	deadline := time.Now().Add(time.Second)
	for m.Running() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	_, err = m.Submit("b", "two", "", runner)
	require.NoError(t, err)
	_, err = m.Submit("c", "three", "", runner)
	require.ErrorIs(t, err, ErrLimitReached)
	close(block)
}

func waitTerminal(t *testing.T, m *Manager, serverID, operationID string) Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if op, ok := m.Get(serverID, operationID); ok && op.State.Terminal() {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not reach terminal state")
	return Operation{}
}
