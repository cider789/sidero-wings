package archives

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestInspectSafeZip(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	writeZip(t, filepath.Join(root, "safe.zip"), map[string][]byte{"world/level.dat": []byte("data"), "world/region/r.0.0.mca": []byte("region")})
	report, err := Inspect(context.Background(), fs, "safe.zip", Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 512, MaximumCompressionRatio: 200})
	require.NoError(t, err)
	require.False(t, report.Unsafe)
	require.Equal(t, 2, report.EntryCount)
	require.Equal(t, 2, report.FileCount)
	require.Equal(t, ".zip", report.Format)
}

func TestInspectFlagsTraversalAndSymbolicLinks(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 1}))
	_, err := tw.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}))
	require.NoError(t, tw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(root, "unsafe.tar"), b.Bytes(), 0o644))
	report, err := Inspect(context.Background(), fs, "unsafe.tar", Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 512, MaximumCompressionRatio: 200})
	require.NoError(t, err)
	require.True(t, report.Unsafe)
	require.Equal(t, 1, report.SymbolicLinkCount)
	require.Contains(t, report.RejectionReasons, "archive_traversal_detected")
	require.Contains(t, report.RejectionReasons, "unsafe_symbolic_link")
}

func TestInspectEnforcesEntryAndSizeLimits(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	writeZip(t, filepath.Join(root, "large.zip"), map[string][]byte{"a": []byte("1234"), "b": []byte("5678")})
	report, err := Inspect(context.Background(), fs, "large.zip", Limits{MaximumEntries: 1, MaximumUncompressedBytes: 6, MaximumSingleEntryBytes: 3, MaximumCompressionRatio: 200})
	require.NoError(t, err)
	require.True(t, report.Unsafe)
	require.Contains(t, report.RejectionReasons, "archive_entry_limit_exceeded")
	require.Contains(t, report.RejectionReasons, "archive_size_limit_exceeded")
}

func archiveTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
	t.Helper()
	c, err := config.NewAtPath("test.yml")
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

func writeZip(t *testing.T, filename string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(filename)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
}
