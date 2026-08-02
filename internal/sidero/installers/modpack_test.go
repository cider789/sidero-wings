package installers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/sidero/archives"
	"github.com/pterodactyl/wings/internal/sidero/download"
)

func TestInstallModpackMergePlacesResolvedFiles(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("mod")) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	result, err := InstallModpack(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), ModpackRequest{OperationKey: "pack-1", Mode: "merge", ConflictPolicy: "replace", Files: []ModpackFile{{URL: remote.URL, Destination: "mods/example.jar"}}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 1024, MaximumCompressionRatio: 200}, nil)
	require.NoError(t, err)
	require.Equal(t, "merge", result.Mode)
	require.FileExists(t, filepath.Join(root, "mods", "example.jar"))
}

func TestInstallModpackCleanRequiresFlagAndRetainsOnlyValidatedPaths(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("new")) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "world"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "world", "level.dat"), []byte("world"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "old.txt"), []byte("old"), 0o644))
	client := download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true})
	_, err := InstallModpack(context.Background(), fs, client, ModpackRequest{OperationKey: "pack-2", Mode: "clean", Files: []ModpackFile{{URL: remote.URL, Destination: "server.jar"}}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)
	require.ErrorIs(t, err, ErrManifestInvalid)
	_, err = InstallModpack(context.Background(), fs, client, ModpackRequest{OperationKey: "pack-2", Mode: "clean", CleanInstall: true, RetainedPaths: []string{"world"}, ConflictPolicy: "replace", Files: []ModpackFile{{URL: remote.URL, Destination: "server.jar"}}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "world", "level.dat"))
	require.FileExists(t, filepath.Join(root, "server.jar"))
	require.NoFileExists(t, filepath.Join(root, "old.txt"))
}

func TestInstallModpackMergeRollsBackEarlierFilesOnCommitFailure(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("new")) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("original"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "z.txt"), 0o755))

	_, err := InstallModpack(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), ModpackRequest{
		OperationKey:   "pack-rollback",
		Mode:           "merge",
		ConflictPolicy: "replace",
		Files: []ModpackFile{
			{URL: remote.URL, Destination: "a.txt"},
			{URL: remote.URL, Destination: "z.txt"},
		},
	}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)
	require.ErrorIs(t, err, ErrDestinationInvalid)
	actual, readErr := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "original", string(actual))
}
