// Package download implements staged, SSRF-resistant remote file downloads.
package download

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var (
	ErrHostDenied       = errors.New("sidero download: remote host denied")
	ErrRedirectDenied   = errors.New("sidero download: redirect denied")
	ErrSizeExceeded     = errors.New("sidero download: size limit exceeded")
	ErrChecksumMismatch = errors.New("sidero download: checksum mismatch")
	ErrConflict         = errors.New("sidero download: destination conflict")
	ErrInvalidRequest   = errors.New("sidero download: invalid request")
	ErrTimeout          = errors.New("sidero download: timed out")
)

type Policy struct {
	MaximumBytes         int64
	MaximumRedirects     int
	ConnectTimeout       time.Duration
	TotalTimeout         time.Duration
	AllowPrivateNetworks bool
	AllowedHosts         []string
	BlockedHosts         []string
}

type Client struct {
	policy   Policy
	resolver *net.Resolver
	http     *http.Client
}

func NewClient(policy Policy) *Client {
	if policy.ConnectTimeout <= 0 {
		policy.ConnectTimeout = 10 * time.Second
	}
	if policy.TotalTimeout <= 0 {
		policy.TotalTimeout = 15 * time.Minute
	}
	c := &Client{policy: policy, resolver: net.DefaultResolver}
	dialer := &net.Dialer{Timeout: policy.ConnectTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DisableCompression: true,
		ForceAttemptHTTP2:  true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return c.dialContext(ctx, dialer, network, address)
		},
	}
	c.http = &http.Client{Transport: transport, Timeout: policy.TotalTimeout, CheckRedirect: c.checkRedirect}
	return c
}

func (c *Client) ValidateURL(ctx context.Context, raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return nil, ErrHostDenied
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrHostDenied
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if hostDenied(host, c.policy.BlockedHosts) {
		return nil, ErrHostDenied
	}
	allowlisted := len(c.policy.AllowedHosts) > 0 && hostAllowed(host, c.policy.AllowedHosts)
	if len(c.policy.AllowedHosts) > 0 && !allowlisted {
		return nil, ErrHostDenied
	}
	addresses, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrHostDenied
	}
	for _, address := range addresses {
		if !c.policy.AllowPrivateNetworks && unsafeIP(address.IP) {
			return nil, ErrHostDenied
		}
	}
	return u, nil
}

func (c *Client) dialContext(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrHostDenied
	}
	canonical := strings.ToLower(strings.TrimSuffix(host, "."))
	allowlisted := hostAllowed(canonical, c.policy.AllowedHosts)
	if hostDenied(canonical, c.policy.BlockedHosts) || len(c.policy.AllowedHosts) > 0 && !allowlisted {
		return nil, ErrHostDenied
	}
	addresses, err := c.resolver.LookupIPAddr(ctx, canonical)
	if err != nil || len(addresses) == 0 {
		return nil, ErrHostDenied
	}
	for _, resolved := range addresses {
		if !c.policy.AllowPrivateNetworks && unsafeIP(resolved.IP) {
			return nil, ErrHostDenied
		}
	}
	// Dial the validated IP rather than resolving the hostname a second time.
	return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > c.policy.MaximumRedirects {
		return ErrRedirectDenied
	}
	if _, err := c.ValidateURL(req.Context(), req.URL.String()); err != nil {
		return ErrRedirectDenied
	}
	req.Header.Del("Authorization")
	req.Header.Del("Cookie")
	req.Header.Del("Proxy-Authorization")
	return nil
}

func unsafeIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return true
	}
	for _, network := range deniedSpecialNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

var deniedSpecialNetworks = func() []*net.IPNet {
	values := []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "100::/64", "2001:db8::/32"}
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, _ := net.ParseCIDR(value)
		networks = append(networks, network)
	}
	return networks
}()

func hostAllowed(host string, entries []string) bool {
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSuffix(entry, "."), host) {
			return true
		}
	}
	return false
}
func hostDenied(host string, entries []string) bool { return hostAllowed(host, entries) }

type ConflictPolicy string

const (
	ConflictFail    ConflictPolicy = "fail"
	ConflictSkip    ConflictPolicy = "skip"
	ConflictReplace ConflictPolicy = "replace"
	ConflictRename  ConflictPolicy = "rename"
)

type Request struct {
	URL               string         `json:"url"`
	Destination       string         `json:"destination"`
	Filename          string         `json:"filename,omitempty"`
	ExpectedSize      *int64         `json:"expected_size,omitempty"`
	ChecksumAlgorithm string         `json:"checksum_algorithm,omitempty"`
	ExpectedChecksum  string         `json:"expected_checksum,omitempty"`
	ConflictPolicy    ConflictPolicy `json:"conflict_policy"`
}

type Result struct {
	OperationID string `json:"-"`
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	Checksum    string `json:"checksum,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"`
}
type ProgressFunc func(float64, string)

func Execute(ctx context.Context, fs *wfs.Filesystem, client *Client, request Request, progress ProgressFunc) (Result, error) {
	u, err := client.ValidateURL(ctx, request.URL)
	if err != nil {
		return Result{}, err
	}
	destination, err := files.NormalizeRelativePath(request.Destination)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	filename := request.Filename
	if filename == "" {
		filename = path.Base(u.EscapedPath())
		if decoded, decodeErr := url.PathUnescape(filename); decodeErr == nil {
			filename = decoded
		}
	}
	if filename == "" || len(filename) > 255 || filename == "." || filename == "/" || path.Base(filename) != filename || strings.Contains(filename, "\\") {
		return Result{}, ErrInvalidRequest
	}
	final, err := files.NormalizeRelativePath(path.Join(destination, filename))
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	if request.ExpectedSize != nil && (*request.ExpectedSize < 0 || *request.ExpectedSize > client.policy.MaximumBytes) {
		return Result{}, ErrSizeExceeded
	}
	if request.ConflictPolicy == "" {
		request.ConflictPolicy = ConflictFail
	}
	if request.ConflictPolicy != ConflictFail && request.ConflictPolicy != ConflictSkip && request.ConflictPolicy != ConflictReplace && request.ConflictPolicy != ConflictRename {
		return Result{}, ErrInvalidRequest
	}
	hasher, expected, err := checksum(request.ChecksumAlgorithm, request.ExpectedChecksum)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}

	opID := uuid.NewString()
	stageDir := path.Join(".sidero", "downloads", opID)
	stage := path.Join(stageDir, "payload.part")
	defer func() { _ = fs.Delete(stageDir) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	req.Header.Set("User-Agent", "Sidero-Wings/"+opID[:8])
	req.Header.Set("Accept-Encoding", "identity")
	response, err := client.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrRedirectDenied) {
			return Result{}, ErrRedirectDenied
		}
		if errors.Is(err, ErrHostDenied) {
			return Result{}, ErrHostDenied
		}
		if networkError, ok := err.(net.Error); ok && networkError.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			return Result{}, ErrTimeout
		}
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Result{}, fmt.Errorf("sidero download: remote status %d", response.StatusCode)
	}
	if response.ContentLength > client.policy.MaximumBytes {
		return Result{}, ErrSizeExceeded
	}
	if request.ExpectedSize != nil && response.ContentLength >= 0 && response.ContentLength != *request.ExpectedSize {
		return Result{}, ErrSizeExceeded
	}
	requiredSpace := int64(-1)
	if response.ContentLength >= 0 {
		requiredSpace = response.ContentLength
	}
	if request.ExpectedSize != nil {
		requiredSpace = *request.ExpectedSize
	}
	if requiredSpace >= 0 {
		if err := fs.HasSpaceFor(requiredSpace); err != nil {
			return Result{}, err
		}
	} else if err := fs.HasSpaceErr(true); err != nil {
		return Result{}, err
	}
	f, err := fs.Touch(stage, ufs.O_WRONLY|ufs.O_CREATE|ufs.O_EXCL)
	if err != nil {
		return Result{}, err
	}
	writer := io.Writer(f)
	if hasher != nil {
		writer = io.MultiWriter(f, hasher)
	}
	reader := &countingReader{reader: io.LimitReader(response.Body, client.policy.MaximumBytes+1), total: response.ContentLength, progress: progress}
	written, copyErr := io.Copy(writer, reader)
	closeErr := f.Close()
	if copyErr != nil {
		return Result{}, copyErr
	}
	if closeErr != nil {
		return Result{}, closeErr
	}
	if written > client.policy.MaximumBytes {
		return Result{}, ErrSizeExceeded
	}
	if request.ExpectedSize != nil && written != *request.ExpectedSize {
		return Result{}, ErrSizeExceeded
	}
	actual := ""
	if hasher != nil {
		actual = hex.EncodeToString(hasher.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return Result{}, ErrChecksumMismatch
		}
	}
	if err := fs.Chown(stage); err != nil {
		return Result{}, err
	}
	final, skipped, err := resolveConflict(fs, final, request.ConflictPolicy)
	if err != nil {
		return Result{}, err
	}
	if skipped {
		return Result{OperationID: opID, Path: final, Bytes: 0, Skipped: true}, nil
	}
	if request.ConflictPolicy == ConflictReplace {
		if _, statErr := fs.UnixFS().Lstat(final); statErr == nil {
			err = fs.AtomicReplace(stage, final)
		} else {
			err = fs.Rename(stage, final)
		}
	} else {
		err = fs.Rename(stage, final)
	}
	if err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(1, "Download completed.")
	}
	return Result{OperationID: opID, Path: final, Bytes: written, Checksum: actual}, nil
}

type countingReader struct {
	reader   io.Reader
	read     int64
	total    int64
	progress ProgressFunc
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	if r.progress != nil && r.total > 0 {
		r.progress(min(float64(r.read)/float64(r.total), 0.99), "Downloading file.")
	}
	return n, err
}

func checksum(algorithm, expected string) (hash.Hash, string, error) {
	algorithm = strings.ToLower(strings.TrimSpace(algorithm))
	expected = strings.ToLower(strings.TrimSpace(expected))
	if algorithm == "" && expected == "" {
		return nil, "", nil
	}
	if expected == "" {
		return nil, "", ErrInvalidRequest
	}
	switch algorithm {
	case "sha1":
		if len(expected) != sha1.Size*2 {
			return nil, "", ErrInvalidRequest
		}
		decoded, err := hex.DecodeString(expected)
		if err != nil {
			return nil, "", err
		}
		if len(decoded) != sha1.Size {
			return nil, "", ErrInvalidRequest
		}
		return sha1.New(), expected, nil
	case "sha256":
		if len(expected) != sha256.Size*2 {
			return nil, "", ErrInvalidRequest
		}
		decoded, err := hex.DecodeString(expected)
		if err != nil {
			return nil, "", err
		}
		if len(decoded) != sha256.Size {
			return nil, "", ErrInvalidRequest
		}
		return sha256.New(), expected, nil
	case "sha512":
		if len(expected) != sha512.Size*2 {
			return nil, "", ErrInvalidRequest
		}
		decoded, err := hex.DecodeString(expected)
		if err != nil {
			return nil, "", err
		}
		if len(decoded) != sha512.Size {
			return nil, "", ErrInvalidRequest
		}
		return sha512.New(), expected, nil
	default:
		return nil, "", ErrInvalidRequest
	}
}

func resolveConflict(fs *wfs.Filesystem, target string, policy ConflictPolicy) (string, bool, error) {
	_, err := fs.UnixFS().Lstat(target)
	if errors.Is(err, ufs.ErrNotExist) {
		return target, false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch policy {
	case ConflictFail:
		return "", false, ErrConflict
	case ConflictSkip:
		return target, true, nil
	case ConflictReplace:
		return target, false, nil
	case ConflictRename:
		ext := path.Ext(target)
		base := strings.TrimSuffix(target, ext)
		for i := 1; i <= 1000; i++ {
			candidate := base + " (" + strconv.Itoa(i) + ")" + ext
			if _, err := fs.UnixFS().Lstat(candidate); errors.Is(err, ufs.ErrNotExist) {
				return candidate, false, nil
			} else if err != nil {
				return "", false, err
			}
		}
		return "", false, ErrConflict
	default:
		return "", false, ErrInvalidRequest
	}
}
