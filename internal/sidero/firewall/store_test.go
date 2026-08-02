package firewall

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStoreRoundTripOmitsRuntimeAllocationDetails(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "firewall.json"))
	rules := []AppliedRule{{Rule: Rule{ID: "abc", AllocationID: 1, Protocol: "tcp", Source: "203.0.113.0/24", Action: "deny"}, ServerID: "server", Destination: "192.0.2.10", Port: 25565}}
	require.NoError(t, store.Save(context.Background(), rules))
	loaded, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, []PersistedRule{{ServerID: "server", Rule: rules[0].Rule}}, loaded)
	contents, err := os.ReadFile(store.path)
	require.NoError(t, err)
	require.NotContains(t, string(contents), "192.0.2.10")
	require.NotContains(t, string(contents), "25565")
}

func TestFileStoreRejectsSymlinkStateFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, []byte("[]"), 0o600))
	store := NewFileStore(filepath.Join(root, "state.json"))
	require.NoError(t, os.Symlink(target, store.path))
	_, err := store.Load()
	require.Error(t, err)
	require.Error(t, store.Save(context.Background(), nil))
}
