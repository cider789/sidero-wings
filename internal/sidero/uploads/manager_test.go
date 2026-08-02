package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestResumableUploadCompletesAndVerifiesContent(t *testing.T) {
	fs, root := uploadTestFilesystem(t)
	content := []byte("abcdefgh")
	sum := sha256.Sum256(content)
	m := NewManager(Config{ChunkSize: 4, SessionExpiry: time.Hour, MaximumUploadBytes: 100, MaximumGlobal: 2, MaximumPerServer: 1})
	session, err := m.Create("server-a", fs, CreateRequest{Destination: "mods", Filename: "file.jar", ExpectedTotalSize: 8, ChecksumAlgorithm: "sha256", ExpectedChecksum: hex.EncodeToString(sum[:]), ConflictPolicy: "fail"})
	require.NoError(t, err)
	require.NoError(t, m.PutChunk(context.Background(), "server-a", session.ID, 1, bytes.NewReader([]byte("efgh")), 4))
	require.NoError(t, m.PutChunk(context.Background(), "server-a", session.ID, 0, bytes.NewReader([]byte("abcd")), 4))
	require.NoError(t, m.PutChunk(context.Background(), "server-a", session.ID, 0, bytes.NewReader([]byte("abcd")), 4))
	result, err := m.Complete(context.Background(), "server-a", session.ID)
	require.NoError(t, err)
	require.Equal(t, "mods/file.jar", result.Path)
	actual, err := os.ReadFile(filepath.Join(root, "mods", "file.jar"))
	require.NoError(t, err)
	require.Equal(t, content, actual)
}

func TestResumableUploadRejectsMismatchedDuplicateAndServer(t *testing.T) {
	fs, _ := uploadTestFilesystem(t)
	m := NewManager(Config{ChunkSize: 4, SessionExpiry: time.Hour, MaximumUploadBytes: 100, MaximumGlobal: 2, MaximumPerServer: 1})
	session, err := m.Create("server-a", fs, CreateRequest{Filename: "x", ExpectedTotalSize: 4, ConflictPolicy: "fail"})
	require.NoError(t, err)
	require.ErrorIs(t, m.PutChunk(context.Background(), "server-b", session.ID, 0, bytes.NewReader([]byte("abcd")), 4), ErrNotFound)
	require.NoError(t, m.PutChunk(context.Background(), "server-a", session.ID, 0, bytes.NewReader([]byte("abcd")), 4))
	require.ErrorIs(t, m.PutChunk(context.Background(), "server-a", session.ID, 0, bytes.NewReader([]byte("wxyz")), 4), ErrChunkInvalid)
}

func TestResumableUploadRejectsTraversalFilenames(t *testing.T) {
	fs, _ := uploadTestFilesystem(t)
	m := NewManager(Config{ChunkSize: 4, SessionExpiry: time.Hour, MaximumUploadBytes: 100, MaximumGlobal: 2, MaximumPerServer: 1})
	for _, filename := range []string{".", "..", "../escape", `..\\escape`} {
		_, err := m.Create("server-a", fs, CreateRequest{Filename: filename, ExpectedTotalSize: 4, ConflictPolicy: "fail"})
		require.ErrorIs(t, err, ErrChunkInvalid, filename)
	}
}

func uploadTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
	t.Helper()
	c, err := config.NewAtPath("test")
	require.NoError(t, err)
	c.AuthenticationToken = "test"
	c.System.User.Uid = os.Getuid()
	c.System.User.Gid = os.Getgid()
	config.Set(c)
	root := t.TempDir()
	fs, err := wfs.New(root, 0, nil)
	require.NoError(t, err)
	return fs, root
}
