package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestDiscoverLatestPathsReturnsOnlyRelativeKnownFiles(t *testing.T) {
	fs, root := processTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "crash-reports"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "logs"), 0o755))
	old := filepath.Join(root, "crash-reports", "old.txt")
	latest := filepath.Join(root, "crash-reports", "latest.txt")
	require.NoError(t, os.WriteFile(old, []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(latest, []byte("new"), 0o644))
	require.NoError(t, os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)))
	require.NoError(t, os.WriteFile(filepath.Join(root, "logs", "latest.log"), []byte("log"), 0o644))
	crash, log := DiscoverLatestPaths(fs, 100)
	require.Equal(t, "crash-reports/latest.txt", crash)
	require.Equal(t, "logs/latest.log", log)
}

func processTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
	t.Helper()
	c, err := config.NewAtPath("test")
	require.NoError(t, err)
	c.AuthenticationToken = "test"
	config.Set(c)
	root := t.TempDir()
	fs, err := wfs.New(root, 0, nil)
	require.NoError(t, err)
	return fs, root
}
