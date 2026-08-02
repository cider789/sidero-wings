package firewall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeBackend struct {
	available bool
	applied   [][]AppliedRule
	failNext  bool
}

func (b *fakeBackend) Available() bool { return b.available }
func (b *fakeBackend) DryRun(context.Context, []AppliedRule) error {
	if !b.available {
		return ErrUnsupported
	}
	return nil
}
func (b *fakeBackend) Apply(_ context.Context, rules []AppliedRule) error {
	b.applied = append(b.applied, append([]AppliedRule(nil), rules...))
	if b.failNext {
		b.failNext = false
		return errors.New("apply failed")
	}
	return nil
}

func TestEngineAddsListsAndExpiresRules(t *testing.T) {
	backend := &fakeBackend{available: true}
	engine := NewEngine(backend, 2, nil)
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	rule, err := engine.Add(context.Background(), "server", "0.0.0.0", 25565, Request{AllocationID: 1, Protocol: "tcp", Source: "203.0.113.0/24", Action: "allow", ExpiresAt: &expires}, now)
	require.NoError(t, err)
	require.Equal(t, rule.ID, engine.List("server")[0].ID)
	require.Equal(t, 0, engine.CleanupExpired(context.Background(), now, 10))
	require.Equal(t, 1, engine.CleanupExpired(context.Background(), expires.Add(time.Second), 10))
	require.Empty(t, engine.List("server"))
}

func TestEngineRollsBackDesiredStateAfterApplyFailure(t *testing.T) {
	backend := &fakeBackend{available: true}
	engine := NewEngine(backend, 2, nil)
	_, err := engine.Add(context.Background(), "server", "192.0.2.10", 25565, Request{AllocationID: 1, Protocol: "tcp", Source: "203.0.113.1", Action: "allow"}, time.Now())
	require.NoError(t, err)
	backend.failNext = true
	_, err = engine.Add(context.Background(), "server", "192.0.2.10", 25566, Request{AllocationID: 2, Protocol: "udp", Source: "203.0.113.2", Action: "deny"}, time.Now())
	require.ErrorIs(t, err, ErrApplyFailed)
	require.Len(t, engine.List("server"), 1)
	require.GreaterOrEqual(t, len(backend.applied), 3)
}

func TestRenderRulesetUsesOnlyDedicatedTableAndStructuredValues(t *testing.T) {
	script, err := RenderRuleset([]AppliedRule{{Rule: Rule{ID: "abc", AllocationID: 1, Protocol: "tcp", Source: "203.0.113.0/24", Action: "allow"}, Destination: "192.0.2.10", Port: 25565}}, true)
	require.NoError(t, err)
	require.Contains(t, script, "table inet sidero_wings")
	require.Contains(t, script, "type filter hook prerouting priority -150; policy accept")
	require.Contains(t, script, "ip daddr 192.0.2.10 ip saddr 203.0.113.0/24 tcp dport 25565 accept")
	require.Contains(t, script, "ip daddr 192.0.2.10 tcp dport 25565 drop")
	require.NotContains(t, script, "iptables")
}

func TestSourcePermittedHonorsAdministratorCIDRPolicy(t *testing.T) {
	require.True(t, SourcePermitted("203.0.113.0/25", []string{"203.0.113.0/24"}, nil))
	require.False(t, SourcePermitted("203.0.113.0/24", []string{"203.0.113.0/25"}, nil))
	require.False(t, SourcePermitted("203.0.113.0/24", nil, []string{"203.0.113.128/25"}))
}
