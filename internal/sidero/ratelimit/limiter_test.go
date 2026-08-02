package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiterEnforcesPerServerAndGlobalRouteLimits(t *testing.T) {
	now := time.Now()
	limiter := New(time.Minute, 3, 2, 100)
	allowed, _ := limiter.Allow("search", "one", now)
	require.True(t, allowed)
	allowed, _ = limiter.Allow("search", "one", now)
	require.True(t, allowed)
	allowed, retry := limiter.Allow("search", "one", now)
	require.False(t, allowed)
	require.Positive(t, retry)
	allowed, _ = limiter.Allow("search", "two", now)
	require.True(t, allowed)
	allowed, _ = limiter.Allow("search", "three", now)
	require.False(t, allowed)
	allowed, _ = limiter.Allow("query", "one", now)
	require.True(t, allowed)
}

func TestLimiterResetsAndCleansBoundedBuckets(t *testing.T) {
	now := time.Now()
	limiter := New(time.Second, 10, 10, 4)
	allowed, _ := limiter.Allow("search", "one", now)
	require.True(t, allowed)
	allowed, _ = limiter.Allow("search", "one", now.Add(2*time.Second))
	require.True(t, allowed)
	require.Positive(t, limiter.Cleanup(now.Add(4*time.Second), 4))
}
