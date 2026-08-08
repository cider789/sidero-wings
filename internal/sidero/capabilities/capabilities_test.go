package capabilities

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

func TestBuildReturnsStableSafeCapabilities(t *testing.T) {
	c, err := config.NewAtPath("test.yml")
	require.NoError(t, err)
	c.Sidero.Firewall.Enabled = false
	r := Build(c.Sidero, "1.13.1")
	require.Equal(t, 1, r.ProtocolVersion)
	require.Equal(t, Version, r.SideroVersion)
	require.True(t, r.Features["operations"])
	require.True(t, r.Features["resolved_modpack"])
	require.True(t, r.Features["archive_inspection"])
	require.True(t, r.Features["safe_extraction"])
	require.True(t, r.Features["world_operations"])
	require.False(t, r.Features["firewall"])
	require.Equal(t, "1", r.FeatureVersions["installers"])
	require.Equal(t, "1", r.FeatureVersions["worlds"])
	require.Equal(t, "1", r.FeatureVersions["archives"])
	require.Equal(t, 3600, r.Limits.OperationRetentionSeconds)
	require.EqualValues(t, 1_073_741_824, r.Limits.MaximumRemoteDownloadBytes)
	require.Equal(t, "compatible", r.Compatibility.State)
}

func TestBuildDisablesAllFeaturesWhenSideroDisabled(t *testing.T) {
	c, err := config.NewAtPath("test.yml")
	require.NoError(t, err)
	c.Sidero.Enabled = false
	r := Build(c.Sidero, "1.13.1")
	for name, enabled := range r.Features {
		require.Falsef(t, enabled, "feature %s should be disabled", name)
	}
	require.Equal(t, "disabled", r.Compatibility.State)
}
