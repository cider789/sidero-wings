package worlds

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/sidero/archives"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestInspectRecognizesJavaAndBedrockWorlds(t *testing.T) {
	fs, root := worldTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "java"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "java", "level.dat"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bedrock", "db"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bedrock", "level.dat"), []byte("x"), 0o644))
	java, err := Inspect(context.Background(), fs, "java", 1024)
	require.NoError(t, err)
	require.Equal(t, "java", java.Format)
	bedrock, err := Inspect(context.Background(), fs, "bedrock", 1024)
	require.NoError(t, err)
	require.Equal(t, "bedrock", bedrock.Format)
}

func TestCloneStagesAndRejectsRunningMutation(t *testing.T) {
	fs, root := worldTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "world"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "world", "level.dat"), []byte("x"), 0o644))
	_, err := Clone(context.Background(), fs, "world", "copy", 1024, true, nil)
	require.ErrorIs(t, err, ErrServerRunning)
	result, err := Clone(context.Background(), fs, "world", "copy", 1024, false, nil)
	require.NoError(t, err)
	require.Equal(t, "copy", result.Path)
	require.FileExists(t, filepath.Join(root, "copy", "level.dat"))
}

func TestImportInvalidWorldPreservesExistingDestination(t *testing.T) {
	fs, root := worldTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "world"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "world", "level.dat"), []byte("original"), 0o644))

	archivePath := filepath.Join(root, "invalid.tar")
	output, err := os.Create(archivePath)
	require.NoError(t, err)
	writer := tar.NewWriter(output)
	content := []byte("not a world")
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "readme.txt", Mode: 0o644, Size: int64(len(content))}))
	_, err = writer.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, output.Close())

	_, err = Import(context.Background(), fs, "invalid.tar", "world", "replace", 1024, false, archives.Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 1024, MaximumCompressionRatio: 200}, nil)
	require.ErrorIs(t, err, ErrInvalidWorld)
	actual, readErr := os.ReadFile(filepath.Join(root, "world", "level.dat"))
	require.NoError(t, readErr)
	require.Equal(t, "original", string(actual))
	require.Empty(t, worldStagingEntries(t, filepath.Join(root, ".sidero", "worlds")))
}

func TestImportRejectsRunningServerWithoutCreatingStaging(t *testing.T) {
	fs, root := worldTestFilesystem(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "world.tar"), []byte("not read"), 0o644))

	_, err := Import(context.Background(), fs, "world.tar", "world", "fail", 1024, true, archives.Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 1024, MaximumCompressionRatio: 200}, nil)

	require.ErrorIs(t, err, ErrServerRunning)
	require.Empty(t, worldStagingEntries(t, filepath.Join(root, ".sidero", "worlds")))
}

func worldTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
	t.Helper()
	c, err := config.NewAtPath("test")
	require.NoError(t, err)
	c.AuthenticationToken = "test"
	c.System.User.Uid, c.System.User.Gid = os.Getuid(), os.Getgid()
	config.Set(c)
	root := t.TempDir()
	fs, err := wfs.New(root, 0, nil)
	require.NoError(t, err)
	return fs, root
}

func worldStagingEntries(t *testing.T, directory string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return entries
}
