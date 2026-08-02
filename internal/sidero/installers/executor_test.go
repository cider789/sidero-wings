package installers

import (
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

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/sidero/download"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestExecuteDownloadsAndAtomicallyPlacesResolvedFiles(t *testing.T) {
	body := []byte("plugin")
	sum := sha256.Sum256(body)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer remote.Close()
	fs, root := installerTestFilesystem(t)
	result, err := Execute(context.Background(), fs, download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}), Request{
		OperationKey: "panel-1", ConflictPolicy: "replace", BackupExisting: true,
		Files: []File{{SourceURL: remote.URL + "/plugin.jar", Destination: "plugins/example.jar", ExpectedSize: int64ptr(int64(len(body))), ChecksumAlgorithm: "sha256", ExpectedChecksum: hex.EncodeToString(sum[:])}},
	}, Limits{MaximumFiles: 10, MaximumDownloadBytes: 1024}, nil)
	require.NoError(t, err)
	require.Len(t, result.Files, 1)
	actual, err := os.ReadFile(filepath.Join(root, "plugins", "example.jar"))
	require.NoError(t, err)
	require.Equal(t, body, actual)
}

func TestExecuteRejectsUnsafeOrDuplicateDestinations(t *testing.T) {
	fs, _ := installerTestFilesystem(t)
	client := download.NewClient(download.Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second})
	_, err := Execute(context.Background(), fs, client, Request{OperationKey: "x", ConflictPolicy: "fail", Files: []File{{SourceURL: "https://example.invalid/a", Destination: "../outside"}}}, Limits{MaximumFiles: 2, MaximumDownloadBytes: 1024}, nil)
	require.ErrorIs(t, err, ErrDestinationInvalid)
	_, err = Execute(context.Background(), fs, client, Request{OperationKey: "x", ConflictPolicy: "fail", Files: []File{{SourceURL: "https://example.invalid/a", Destination: "mods/a.jar"}, {SourceURL: "https://example.invalid/b", Destination: "mods/a.jar"}}}, Limits{MaximumFiles: 2, MaximumDownloadBytes: 1024}, nil)
	require.ErrorIs(t, err, ErrManifestInvalid)
}

func installerTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
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

func int64ptr(v int64) *int64 { return &v }
