// Package query implements bounded node-side game status providers.
package query

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrUnsupported = errors.New("sidero query: unsupported provider")
	ErrTimeout     = errors.New("sidero query: timeout")
	ErrOffline     = errors.New("sidero query: offline")
	ErrMalformed   = errors.New("sidero query: malformed response")
)

type Target struct {
	Host string
	Port int
}

type Players struct {
	Online  int      `json:"online"`
	Maximum int      `json:"maximum"`
	Sample  []string `json:"sample"`
}

type Result struct {
	Supported bool      `json:"supported"`
	Online    bool      `json:"online"`
	Provider  string    `json:"provider"`
	Players   Players   `json:"players"`
	Version   string    `json:"version,omitempty"`
	Protocol  int       `json:"protocol,omitempty"`
	MOTD      string    `json:"motd,omitempty"`
	LatencyMS int64     `json:"latency_ms"`
	QueriedAt time.Time `json:"queried_at"`
	Stale     bool      `json:"stale"`
}

type Config struct {
	Timeout        time.Duration
	Cache          time.Duration
	Stale          time.Duration
	OfflineBackoff time.Duration
	MaximumRetries int
}

type QueryFunc func(context.Context, string, Target) (Result, error)

type cacheEntry struct {
	result       Result
	expiresAt    time.Time
	staleUntil   time.Time
	backoffUntil time.Time
	failures     int
}

type Manager struct {
	cfg      Config
	query    QueryFunc
	mu       sync.Mutex
	cache    map[string]cacheEntry
	inflight map[string]chan struct{}
}

func NewManager(cfg Config, query QueryFunc) *Manager {
	if query == nil {
		query = defaultQuery
	}
	return &Manager{cfg: cfg, query: query, cache: map[string]cacheEntry{}, inflight: map[string]chan struct{}{}}
}

func (m *Manager) Query(ctx context.Context, serverID string, allocationID int64, provider string, target Target) (Result, error) {
	key := fmt.Sprintf("%s\x00%d\x00%s", serverID, allocationID, provider)
	now := time.Now()
	m.mu.Lock()
	entry, hasCache := m.cache[key]
	if hasCache && now.Before(entry.expiresAt) {
		m.mu.Unlock()
		return entry.result, nil
	}
	if wait, ok := m.inflight[key]; ok {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-wait:
			return m.Query(ctx, serverID, allocationID, provider, target)
		}
	}
	if hasCache && now.Before(entry.backoffUntil) {
		if now.Before(entry.staleUntil) {
			entry.result.Stale = true
			m.mu.Unlock()
			return entry.result, nil
		}
		m.mu.Unlock()
		return Result{}, ErrOffline
	}
	wait := make(chan struct{})
	m.inflight[key] = wait
	m.mu.Unlock()

	var result Result
	var err error
	for attempt := 0; attempt <= m.cfg.MaximumRetries; attempt++ {
		queryCtx, cancel := context.WithTimeout(ctx, m.cfg.Timeout)
		result, err = m.query(queryCtx, provider, target)
		cancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}

	m.mu.Lock()
	delete(m.inflight, key)
	close(wait)
	if err == nil {
		result.Stale = false
		if result.QueriedAt.IsZero() {
			result.QueriedAt = time.Now().UTC()
		}
		m.cache[key] = cacheEntry{result: result, expiresAt: time.Now().Add(m.cfg.Cache), staleUntil: time.Now().Add(m.cfg.Stale)}
		m.mu.Unlock()
		return result, nil
	}
	entry = m.cache[key]
	entry.failures++
	backoff := m.cfg.OfflineBackoff
	if entry.failures >= 3 {
		backoff *= 2
	}
	entry.backoffUntil = time.Now().Add(backoff)
	m.cache[key] = entry
	if !entry.result.QueriedAt.IsZero() && time.Now().Before(entry.staleUntil) {
		entry.result.Stale = true
		m.mu.Unlock()
		return entry.result, nil
	}
	m.mu.Unlock()
	if errors.Is(err, context.DeadlineExceeded) {
		return Result{}, ErrTimeout
	}
	return Result{}, err
}

func (m *Manager) Cleanup(now time.Time, maximum int) int {
	if maximum < 1 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for key, entry := range m.cache {
		if removed >= maximum {
			break
		}
		if _, active := m.inflight[key]; active {
			continue
		}
		retainedUntil := entry.staleUntil
		if entry.backoffUntil.After(retainedUntil) {
			retainedUntil = entry.backoffUntil
		}
		if !now.Before(retainedUntil) {
			delete(m.cache, key)
			removed++
		}
	}
	return removed
}

func defaultQuery(ctx context.Context, provider string, target Target) (Result, error) {
	switch provider {
	case "minecraft-java":
		return QueryJava(ctx, target, 12)
	case "minecraft-bedrock":
		return QueryBedrock(ctx, target)
	default:
		return Result{}, ErrUnsupported
	}
}
