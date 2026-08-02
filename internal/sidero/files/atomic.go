package files

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	iofs "io/fs"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var ErrConditionalConflict = errors.New("sidero files: conditional write conflict")

var conditionalWriteLocks [256]sync.Mutex

type ConditionalWriteRequest struct {
	Path                  string `json:"path"`
	ExpectedCurrentSHA256 string `json:"expected_current_sha256"`
	Content               string `json:"content"`
	CreateBackup          bool   `json:"create_backup"`
}
type ConditionalWriteResult struct {
	PreviousSHA256 string    `json:"previous_sha256"`
	NewSHA256      string    `json:"new_sha256"`
	Backup         string    `json:"backup,omitempty"`
	ModifiedAt     time.Time `json:"modified_at"`
}

func ConditionalWrite(ctx context.Context, filesystem *wfs.Filesystem, request ConditionalWriteRequest, maximumBytes int64) (ConditionalWriteResult, error) {
	rel, err := NormalizeClientPath(request.Path)
	if err != nil || rel == "." || int64(len(request.Content)) > maximumBytes {
		return ConditionalWriteResult{}, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return ConditionalWriteResult{}, err
	}
	lockHash := sha256.Sum256([]byte(filesystem.Path() + "\x00" + rel))
	lock := &conditionalWriteLocks[int(lockHash[0])]
	lock.Lock()
	defer lock.Unlock()
	current, stat, err := filesystem.File(rel)
	if err != nil {
		return ConditionalWriteResult{}, err
	}
	defer current.Close()
	if stat.IsDir() || stat.Size() > maximumBytes {
		return ConditionalWriteResult{}, ErrInvalidPath
	}
	requiredSpace := int64(len(request.Content))
	if request.CreateBackup {
		requiredSpace += stat.Size()
	}
	if err := filesystem.HasSpaceFor(requiredSpace); err != nil {
		return ConditionalWriteResult{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(current, maximumBytes+1)); err != nil {
		return ConditionalWriteResult{}, err
	}
	previous := hex.EncodeToString(h.Sum(nil))
	expected := strings.ToLower(strings.TrimSpace(request.ExpectedCurrentSHA256))
	if len(expected) != sha256.Size*2 {
		return ConditionalWriteResult{}, ErrConditionalConflict
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size || subtle.ConstantTimeCompare([]byte(previous), []byte(expected)) != 1 {
		return ConditionalWriteResult{}, ErrConditionalConflict
	}
	_ = decoded
	opID := uuid.NewString()
	stageDir := path.Join(".sidero", "writes", opID)
	stage := path.Join(stageDir, "replacement.part")
	defer func() { _ = filesystem.Delete(stageDir) }()
	if err := filesystem.Write(stage, strings.NewReader(request.Content), int64(len(request.Content)), stat.Mode().Perm()); err != nil {
		return ConditionalWriteResult{}, err
	}
	backup := ""
	if request.CreateBackup {
		if _, err := current.Seek(0, io.SeekStart); err != nil {
			return ConditionalWriteResult{}, err
		}
		backup = path.Join(".sidero", "backups", opID, path.Base(rel))
		if err := filesystem.Write(backup, current, stat.Size(), stat.Mode().Perm()); err != nil {
			return ConditionalWriteResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ConditionalWriteResult{}, err
	}
	currentSHA, err := checksumCurrentFile(filesystem, rel, maximumBytes)
	if err != nil {
		return ConditionalWriteResult{}, err
	}
	if subtle.ConstantTimeCompare([]byte(currentSHA), []byte(expected)) != 1 {
		return ConditionalWriteResult{}, ErrConditionalConflict
	}
	if err := filesystem.AtomicReplace(stage, rel); err != nil {
		return ConditionalWriteResult{}, err
	}
	newSum := sha256.Sum256([]byte(request.Content))
	return ConditionalWriteResult{PreviousSHA256: previous, NewSHA256: hex.EncodeToString(newSum[:]), Backup: backup, ModifiedAt: time.Now().UTC()}, nil
}

func checksumCurrentFile(filesystem *wfs.Filesystem, rel string, maximumBytes int64) (string, error) {
	current, stat, err := filesystem.File(rel)
	if err != nil {
		return "", err
	}
	defer current.Close()
	if !stat.Mode().IsRegular() || stat.Size() > maximumBytes {
		return "", ErrInvalidPath
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(current, maximumBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type ProbeRequest struct {
	Paths         []string `json:"paths"`
	IncludeSHA256 bool     `json:"include_sha256"`
}
type ProbeResult struct {
	Path       string     `json:"path"`
	Exists     bool       `json:"exists"`
	Type       string     `json:"type,omitempty"`
	Size       int64      `json:"size,omitempty"`
	ModifiedAt *time.Time `json:"modified_at,omitempty"`
	Symlink    bool       `json:"symlink,omitempty"`
	SHA256     string     `json:"sha256,omitempty"`
}

func Probe(ctx context.Context, filesystem *wfs.Filesystem, request ProbeRequest, maximumPaths int, checksumThreshold int64) ([]ProbeResult, error) {
	if len(request.Paths) == 0 || len(request.Paths) > maximumPaths {
		return nil, ErrLimitReached
	}
	results := make([]ProbeResult, 0, len(request.Paths))
	for _, requested := range request.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, err := NormalizeClientPath(requested)
		if err != nil {
			return nil, err
		}
		info, err := filesystem.UnixFS().Lstat(rel)
		if errors.Is(err, ufs.ErrNotExist) {
			results = append(results, ProbeResult{Path: rel})
			continue
		}
		if err != nil {
			return nil, err
		}
		modified := info.ModTime().UTC()
		result := ProbeResult{Path: rel, Exists: true, Size: info.Size(), ModifiedAt: &modified}
		if info.Mode()&iofs.ModeSymlink != 0 {
			result.Type, result.Symlink = "symlink", true
			results = append(results, result)
			continue
		}
		if info.IsDir() {
			result.Type = "directory"
		} else if info.Mode().IsRegular() {
			result.Type = "file"
		}
		if request.IncludeSHA256 && result.Type == "file" && info.Size() <= checksumThreshold {
			f, _, err := filesystem.File(rel)
			if err != nil {
				return nil, err
			}
			h := sha256.New()
			_, copyErr := io.Copy(h, io.LimitReader(f, checksumThreshold+1))
			closeErr := f.Close()
			if copyErr != nil {
				return nil, copyErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			result.SHA256 = hex.EncodeToString(h.Sum(nil))
		}
		results = append(results, result)
	}
	return results, nil
}
