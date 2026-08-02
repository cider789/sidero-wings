package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

func TestSideroConfigurationDefaults(t *testing.T) {
	c, err := NewAtPath("test.yml")
	require.NoError(t, err)
	require.True(t, c.Sidero.Enabled)
	require.Equal(t, 1, c.Sidero.ProtocolVersion)
	require.Equal(t, 16, c.Sidero.Operations.MaximumConcurrentGlobal)
	require.Equal(t, 3, c.Sidero.Operations.MaximumConcurrentPerServer)
	require.EqualValues(t, 1_073_741_824, c.Sidero.RemoteDownload.MaximumBytes)
	require.False(t, c.Sidero.RemoteDownload.AllowPrivateNetworks)
	require.False(t, c.Sidero.Archives.AllowSymbolicLinks)
	require.False(t, c.Sidero.Firewall.Enabled)
	require.Equal(t, 240, c.Sidero.RateLimits.MaximumGlobalPerRoute)
	require.Equal(t, 30, c.Sidero.RateLimits.MaximumPerServerPerRoute)
	require.NoError(t, c.Sidero.Validate())
}

func TestSideroConfigurationRejectsInvalidLimits(t *testing.T) {
	c, err := NewAtPath("test.yml")
	require.NoError(t, err)
	c.Sidero.Operations.MaximumConcurrentPerServer = c.Sidero.Operations.MaximumConcurrentGlobal + 1
	err = c.Sidero.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "maximum_concurrent_per_server")
}

func TestSideroConfigurationRejectsInvalidProtocolVersion(t *testing.T) {
	c, err := NewAtPath("test.yml")
	require.NoError(t, err)
	c.Sidero.ProtocolVersion = 0
	require.ErrorContains(t, c.Sidero.Validate(), "protocol_version")
}

func TestSideroConfigurationYAMLOverridesDefaults(t *testing.T) {
	c, err := NewAtPath("test.yml")
	require.NoError(t, err)
	err = yaml.NewDecoder(strings.NewReader(`
sidero:
  enabled: false
  operations:
    maximum_concurrent_global: 8
    maximum_concurrent_per_server: 2
`)).Decode(c)
	require.NoError(t, err)
	require.False(t, c.Sidero.Enabled)
	require.Equal(t, 8, c.Sidero.Operations.MaximumConcurrentGlobal)
	require.Equal(t, 2, c.Sidero.Operations.MaximumConcurrentPerServer)
	require.NoError(t, c.Sidero.Validate())
}
