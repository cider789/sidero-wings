// Package ratelimit provides a bounded in-memory fixed-window limiter for
// Sidero's authenticated, server-scoped API categories.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	started time.Time
	count   int
}

type Limiter struct {
	mu               sync.Mutex
	window           time.Duration
	maximumGlobal    int
	maximumPerServer int
	maximumBuckets   int
	global           map[string]bucket
	servers          map[string]bucket
}

func New(window time.Duration, maximumGlobal, maximumPerServer, maximumBuckets int) *Limiter {
	if window <= 0 {
		window = time.Minute
	}
	if maximumGlobal < 1 {
		maximumGlobal = 1
	}
	if maximumPerServer < 1 {
		maximumPerServer = 1
	}
	if maximumBuckets < 1 {
		maximumBuckets = 1
	}
	return &Limiter{window: window, maximumGlobal: maximumGlobal, maximumPerServer: maximumPerServer, maximumBuckets: maximumBuckets, global: map[string]bucket{}, servers: map[string]bucket{}}
}

func (l *Limiter) Allow(category, serverID string, now time.Time) (bool, time.Duration) {
	if category == "" || serverID == "" {
		return false, l.window
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	global := currentBucket(l.global[category], now, l.window)
	key := category + "\x00" + serverID
	server := currentBucket(l.servers[key], now, l.window)
	if global.count >= l.maximumGlobal || server.count >= l.maximumPerServer {
		retry := global.started.Add(l.window).Sub(now)
		if serverRetry := server.started.Add(l.window).Sub(now); server.count >= l.maximumPerServer && serverRetry > retry {
			retry = serverRetry
		}
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}
	if _, exists := l.servers[key]; !exists && len(l.servers) >= l.maximumBuckets {
		return false, l.window
	}
	global.count++
	server.count++
	l.global[category] = global
	l.servers[key] = server
	return true, 0
}

func (l *Limiter) Cleanup(now time.Time, maximum int) int {
	if maximum < 1 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := 0
	staleBefore := now.Add(-2 * l.window)
	for key, value := range l.servers {
		if removed >= maximum {
			break
		}
		if !value.started.After(staleBefore) {
			delete(l.servers, key)
			removed++
		}
	}
	for key, value := range l.global {
		if removed >= maximum {
			break
		}
		if !value.started.After(staleBefore) {
			delete(l.global, key)
			removed++
		}
	}
	return removed
}

func currentBucket(value bucket, now time.Time, window time.Duration) bucket {
	if value.started.IsZero() || !now.Before(value.started.Add(window)) {
		return bucket{started: now}
	}
	return value
}
