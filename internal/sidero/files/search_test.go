package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestNormalizeRelativePathRejectsEscapes(t *testing.T) {
	for _, value := range []string{"/etc/passwd", "../secret", "a/../../secret", ".sidero", ".sidero/uploads/file", `C:\\Windows\\system.ini`, "C:/Windows/system.ini", `..\\secret`, "a\x00b"} {
		_, err := NormalizeClientPath(value)
		require.Error(t, err, value)
	}
	require.Equal(t, "a/b", mustNormalize(t, "./a//b"))
	require.Equal(t, ".", mustNormalize(t, ""))
}

func TestSearchFindsNamesDeterministicallyAndHonorsDepth(t *testing.T) {
	fs, root := newTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mods", "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mods", "Example.JAR"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mods", "nested", "example.txt"), []byte("two"), 0o644))

	result, err := Search(context.Background(), fs, SearchRequest{Root: ".", Query: "example", Recursive: true, IncludeFiles: true, MaximumDepth: 1, MaximumResults: 10, MaximumEntries: 100})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	require.Equal(t, "mods/Example.JAR", result.Entries[0].Path)

	result, err = Search(context.Background(), fs, SearchRequest{Root: ".", Query: "example", Recursive: true, CaseSensitive: true, IncludeFiles: true, MaximumDepth: 4, MaximumResults: 10, MaximumEntries: 100})
	require.NoError(t, err)
	require.Equal(t, "mods/nested/example.txt", result.Entries[0].Path)
}

func TestSearchDoesNotFollowSymbolicLinks(t *testing.T) {
	fs, root := newTestFilesystem(t)
	external := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(external, "secret.txt"), []byte("secret"), 0o644))
	require.NoError(t, os.Symlink(external, filepath.Join(root, "escape")))
	result, err := Search(context.Background(), fs, SearchRequest{Root: ".", Query: "secret", Recursive: true, IncludeFiles: true, MaximumDepth: 8, MaximumResults: 10, MaximumEntries: 100})
	require.NoError(t, err)
	require.Empty(t, result.Entries)
}

func TestContentSearchSkipsBinaryAndReturnsLineNumbers(t *testing.T) {
	fs, root := newTestFilesystem(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.log"), []byte("first\nNeedle value\nlast\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "binary.dat"), []byte{'N', 0, 'e', 'e', 'd', 'l', 'e'}, 0o644))
	result, err := ContentSearch(context.Background(), fs, ContentSearchRequest{Root: ".", Query: "needle", MaximumFileSize: 1024, MaximumMatchesPerFile: 5, MaximumTotalMatches: 10, MaximumExcerptBytes: 64})
	require.NoError(t, err)
	require.Len(t, result.Matches, 1)
	require.Equal(t, "server.log", result.Matches[0].Path)
	require.Equal(t, 2, result.Matches[0].Line)
	require.Equal(t, "Needle value", result.Matches[0].Excerpt)
}

func TestSearchRejectsOversizedClientFields(t *testing.T) {
	fs, _ := newTestFilesystem(t)
	_, err := Search(context.Background(), fs, SearchRequest{Root: ".", Query: string(make([]byte, maximumSearchQueryBytes+1)), MaximumDepth: 1, MaximumResults: 1, MaximumEntries: 1})
	require.ErrorIs(t, err, ErrInvalidPath)
	filters := make([]string, maximumExtensionFilters+1)
	_, err = ContentSearch(context.Background(), fs, ContentSearchRequest{Root: ".", Query: "x", AllowedExtensions: filters, MaximumFileSize: 1, MaximumMatchesPerFile: 1, MaximumTotalMatches: 1, MaximumExcerptBytes: 1})
	require.ErrorIs(t, err, ErrInvalidPath)
}

func newTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
	t.Helper()
	c, err := config.NewAtPath("test.yml")
	require.NoError(t, err)
	c.AuthenticationToken = "test-token"
	c.System.User.Uid = os.Getuid()
	c.System.User.Gid = os.Getgid()
	config.Set(c)
	root := t.TempDir()
	fs, err := wfs.New(root, 0, nil)
	require.NoError(t, err)
	return fs, root
}

func mustNormalize(t *testing.T, value string) string {
	t.Helper()
	v, err := NormalizeClientPath(value)
	require.NoError(t, err)
	return v
}
