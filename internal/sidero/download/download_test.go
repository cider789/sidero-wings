package download

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

func TestValidateURLRejectsUnsupportedAndPrivateTargets(t *testing.T) {
	p := Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second}
	_, err := NewClient(p).ValidateURL(context.Background(), "file:///etc/passwd")
	require.ErrorIs(t, err, ErrHostDenied)
	_, err = NewClient(p).ValidateURL(context.Background(), "http://127.0.0.1/file")
	require.ErrorIs(t, err, ErrHostDenied)
	_, err = NewClient(p).ValidateURL(context.Background(), "http://[::1]/file")
	require.ErrorIs(t, err, ErrHostDenied)
	_, err = NewClient(p).ValidateURL(context.Background(), "http://100.64.0.1/file")
	require.ErrorIs(t, err, ErrHostDenied)
	_, err = NewClient(p).ValidateURL(context.Background(), "https://example.com/"+strings.Repeat("x", 8192))
	require.ErrorIs(t, err, ErrHostDenied)
}

func TestExecuteStagesVerifiesAndCommits(t *testing.T) {
	body := []byte("verified content")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	fs, root := downloadTestFilesystem(t)
	p := Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: 2 * time.Second, AllowPrivateNetworks: true}
	result, err := Execute(context.Background(), fs, NewClient(p), Request{URL: server.URL + "/artifact", Destination: "mods", Filename: "example.jar", ChecksumAlgorithm: "sha256", ExpectedChecksum: hex.EncodeToString(sum[:]), ConflictPolicy: ConflictFail}, nil)
	require.NoError(t, err)
	require.Equal(t, "mods/example.jar", result.Path)
	require.EqualValues(t, len(body), result.Bytes)
	actual, err := os.ReadFile(filepath.Join(root, "mods", "example.jar"))
	require.NoError(t, err)
	require.Equal(t, body, actual)
	require.NoDirExists(t, filepath.Join(root, ".sidero", "downloads", result.OperationID))
}

func TestExecuteSupportsSHA1Checksum(t *testing.T) {
	body := []byte("vanilla server jar")
	sum := sha1.Sum(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	fs, root := downloadTestFilesystem(t)
	p := Policy{MaximumBytes: 1024, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: 2 * time.Second, AllowPrivateNetworks: true}
	result, err := Execute(context.Background(), fs, NewClient(p), Request{URL: server.URL + "/server.jar", Filename: "server.jar", ChecksumAlgorithm: "sha1", ExpectedChecksum: hex.EncodeToString(sum[:]), ConflictPolicy: ConflictFail}, nil)
	require.NoError(t, err)
	require.Equal(t, "server.jar", result.Path)
	actual, err := os.ReadFile(filepath.Join(root, "server.jar"))
	require.NoError(t, err)
	require.Equal(t, body, actual)
}

func TestExecuteRejectsRedirectToPrivateTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer target.Close()
	privateTargetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, privateTargetURL, http.StatusFound) }))
	defer source.Close()
	fs, _ := downloadTestFilesystem(t)
	p := Policy{MaximumBytes: 1024, MaximumRedirects: 2, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true, AllowedHosts: []string{"127.0.0.1"}}
	_, err := Execute(context.Background(), fs, NewClient(p), Request{URL: source.URL, Filename: "x", ConflictPolicy: ConflictFail}, nil)
	require.ErrorIs(t, err, ErrRedirectDenied)
}

func TestExecuteEnforcesSizeAndCleansTemporaryFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("too large")) }))
	defer server.Close()
	fs, root := downloadTestFilesystem(t)
	p := Policy{MaximumBytes: 3, MaximumRedirects: 1, ConnectTimeout: time.Second, TotalTimeout: time.Second, AllowPrivateNetworks: true}
	_, err := Execute(context.Background(), fs, NewClient(p), Request{URL: server.URL, Filename: "x", ConflictPolicy: ConflictFail}, nil)
	require.ErrorIs(t, err, ErrSizeExceeded)
	_, statErr := os.Stat(filepath.Join(root, "x"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func downloadTestFilesystem(t *testing.T) (*wfs.Filesystem, string) {
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
