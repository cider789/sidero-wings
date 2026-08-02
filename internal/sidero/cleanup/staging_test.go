package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestStagingCleanupDeletesOnlyOldGeneratedDirectories(t *testing.T) {
	filesystem, root := cleanupTestFilesystem(t)
	oldID, recentID := uuid.NewString(), uuid.NewString()
	old := filepath.Join(root, ".sidero", "downloads", oldID)
	recent := filepath.Join(root, ".sidero", "downloads", recentID)
	backup := filepath.Join(root, ".sidero", "backups", uuid.NewString())
	require.NoError(t, os.MkdirAll(old, 0o755))
	require.NoError(t, os.MkdirAll(recent, 0o755))
	require.NoError(t, os.MkdirAll(backup, 0o755))
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(old, oldTime, oldTime))

	removed := Staging(context.Background(), filesystem, time.Now(), 7*24*time.Hour, 10)
	require.Equal(t, 1, removed)
	require.NoDirExists(t, old)
	require.DirExists(t, recent)
	require.DirExists(t, backup)
}

func TestStagingCleanupNeverFollowsSymlink(t *testing.T) {
	filesystem, root := cleanupTestFilesystem(t)
	target := filepath.Join(root, "customer")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".sidero", "archives"), 0o755))
	require.NoError(t, os.Mkdir(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(target, filepath.Join(root, ".sidero", "archives", uuid.NewString())))
	require.Zero(t, Staging(context.Background(), filesystem, time.Now().Add(8*24*time.Hour), 7*24*time.Hour, 10))
	require.FileExists(t, filepath.Join(target, "keep"))
}

func cleanupTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
	t.Helper()
	c, err := config.NewAtPath("test")
	require.NoError(t, err)
	c.AuthenticationToken = "test"
	c.System.User.Uid, c.System.User.Gid = os.Getuid(), os.Getgid()
	config.Set(c)
	root := t.TempDir()
	filesystem, err := wfs.New(root, 0, nil)
	require.NoError(t, err)
	return filesystem, root
}
