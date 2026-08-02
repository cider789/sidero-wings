package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConditionalWriteDetectsConflictAndAtomicallyReplaces(t *testing.T) {
	fs, root := newTestFilesystem(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.properties"), []byte("old"), 0o644))
	wrong := sha256.Sum256([]byte("different"))
	_, err := ConditionalWrite(context.Background(), fs, ConditionalWriteRequest{Path: "server.properties", ExpectedCurrentSHA256: hex.EncodeToString(wrong[:]), Content: "new"}, 1024)
	require.ErrorIs(t, err, ErrConditionalConflict)
	old := sha256.Sum256([]byte("old"))
	result, err := ConditionalWrite(context.Background(), fs, ConditionalWriteRequest{Path: "server.properties", ExpectedCurrentSHA256: hex.EncodeToString(old[:]), Content: "new", CreateBackup: true}, 1024)
	require.NoError(t, err)
	require.NotEmpty(t, result.Backup)
	actual, err := os.ReadFile(filepath.Join(root, "server.properties"))
	require.NoError(t, err)
	require.Equal(t, "new", string(actual))
}

func TestProbeIsBoundedAndDoesNotFollowSymlinks(t *testing.T) {
	fs, root := newTestFilesystem(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "small.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.Symlink("small.txt", filepath.Join(root, "link.txt")))
	result, err := Probe(context.Background(), fs, ProbeRequest{Paths: []string{"small.txt", "missing", "link.txt"}, IncludeSHA256: true}, 3, 16)
	require.NoError(t, err)
	require.True(t, result[0].Exists)
	require.NotEmpty(t, result[0].SHA256)
	require.False(t, result[1].Exists)
	require.True(t, result[2].Symlink)
	require.Empty(t, result[2].SHA256)
	_, err = Probe(context.Background(), fs, ProbeRequest{Paths: []string{"a", "b"}}, 1, 16)
	require.ErrorIs(t, err, ErrLimitReached)
}
