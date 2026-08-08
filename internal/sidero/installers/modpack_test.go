package installers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	content := []byte("mod")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	result, err := InstallModpack(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), ModpackRequest{OperationKey: "pack-1", Mode: "merge", ConflictPolicy: "replace", Files: []ModpackFile{modpackFile(remote.URL, "mods/example.jar", content)}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 1024, MaximumCompressionRatio: 200}, nil)
	require.NoError(t, err)
	require.Equal(t, "merge", result.Mode)
	require.FileExists(t, filepath.Join(root, "mods", "example.jar"))
}

func TestInstallModpackCleanRequiresFlagAndRetainsOnlyValidatedPaths(t *testing.T) {
	content := []byte("new")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "world"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "world", "level.dat"), []byte("world"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "old.txt"), []byte("old"), 0o644))
	client := download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true})
	_, err := InstallModpack(context.Background(), fs, client, ModpackRequest{OperationKey: "pack-2", Mode: "clean", Files: []ModpackFile{modpackFile(remote.URL, "server.jar", content)}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)
	require.ErrorIs(t, err, ErrManifestInvalid)
	result, err := InstallModpack(context.Background(), fs, client, ModpackRequest{OperationKey: "pack-2", Mode: "clean", CleanInstall: true, BackupExisting: true, RetainedPaths: []string{"world"}, ConflictPolicy: "replace", Files: []ModpackFile{modpackFile(remote.URL, "server.jar", content)}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)
	require.NoError(t, err)
	require.Contains(t, result.Backup, ".sidero/backups/modpacks/")
	require.FileExists(t, filepath.Join(root, "world", "level.dat"))
	require.FileExists(t, filepath.Join(root, "server.jar"))
	require.NoFileExists(t, filepath.Join(root, "old.txt"))
}

func TestInstallModpackMergeRollsBackEarlierFilesOnCommitFailure(t *testing.T) {
	content := []byte("new")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("original"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "z.txt"), 0o755))

	_, err := InstallModpack(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), ModpackRequest{
		OperationKey:   "pack-rollback",
		Mode:           "merge",
		ConflictPolicy: "replace",
		Files: []ModpackFile{
			modpackFile(remote.URL, "a.txt", content),
			modpackFile(remote.URL, "z.txt", content),
		},
	}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)
	require.ErrorIs(t, err, ErrDestinationInvalid)
	actual, readErr := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "original", string(actual))
}

func TestInstallModpackRejectsResolvedFileWithoutChecksumBeforeMutation(t *testing.T) {
	requests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("mod"))
	}))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)

	_, err := InstallModpack(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), ModpackRequest{OperationKey: "pack-unchecked", Mode: "merge", ConflictPolicy: "replace", Files: []ModpackFile{{URL: remote.URL, Destination: "mods/example.jar", ExpectedSize: modpackInt64ptr(3)}}}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{}, nil)

	require.ErrorIs(t, err, ErrManifestInvalid)
	require.Zero(t, requests)
	require.Empty(t, stagingEntries(t, filepath.Join(root, ".sidero", "modpacks")))
}

func TestInstallModpackRejectsOverrideArchiveExceedingArchiveLimits(t *testing.T) {
	archive := modpackArchive(t, map[string][]byte{"config/large.cfg": bytes.Repeat([]byte("x"), 32)})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)

	_, err := InstallModpack(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), ModpackRequest{OperationKey: "pack-oversized-overrides", Mode: "merge", ConflictPolicy: "replace", OverridesArchive: modpackOverride(remote.URL, archive)}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, archives.Limits{MaximumEntries: 10, MaximumUncompressedBytes: 16, MaximumSingleEntryBytes: 16, MaximumCompressionRatio: 200}, nil)

	require.ErrorIs(t, err, archives.ErrSizeLimit)
	require.Empty(t, stagingEntries(t, filepath.Join(root, ".sidero", "modpacks")))
}

func modpackFile(url, destination string, content []byte) ModpackFile {
	size := int64(len(content))
	sum := sha256.Sum256(content)
	return ModpackFile{URL: url, Destination: destination, ExpectedSize: &size, ChecksumAlgorithm: "sha256", Checksum: hex.EncodeToString(sum[:])}
}

func modpackOverride(url string, content []byte) *OverridesArchive {
	size := int64(len(content))
	sum := sha256.Sum256(content)
	return &OverridesArchive{URL: url, ExpectedSize: &size, ChecksumAlgorithm: "sha256", Checksum: hex.EncodeToString(sum[:])}
}

func modpackArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, content := range entries {
		file, err := writer.Create(name)
		require.NoError(t, err)
		_, err = file.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return out.Bytes()
}

func modpackInt64ptr(value int64) *int64 { return &value }

func stagingEntries(t *testing.T, directory string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return entries
}
