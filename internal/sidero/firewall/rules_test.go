package firewall

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateProducesDeterministicStructuredRule(t *testing.T) {
	now := time.Now().UTC()
	request := Request{AllocationID: 12, Protocol: "tcp", Source: "203.0.113.0/24", Action: "allow", ExpiresAt: ptrTime(now.Add(time.Hour)), Description: "temporary access"}
	rule, err := Validate("server", request, now)
	require.NoError(t, err)
	require.NotEmpty(t, rule.ID)
	require.NotContains(t, rule.ID, "server")
	again, err := Validate("server", request, now)
	require.NoError(t, err)
	require.Equal(t, rule.ID, again.ID)
}

func TestValidateRejectsRawOrUnsafeFields(t *testing.T) {
	for _, request := range []Request{{Protocol: "all", Source: "203.0.113.1", Action: "allow"}, {Protocol: "tcp", Source: "not-an-ip", Action: "allow"}, {Protocol: "tcp", Source: "203.0.113.1", Action: "drop; flush ruleset"}} {
		_, err := Validate("server", request, time.Now())
		require.ErrorIs(t, err, ErrInvalidRule)
	}
}

func ptrTime(v time.Time) *time.Time { return &v }
