// Package installers executes Panel-resolved installation specifications. It
// deliberately contains no provider discovery, shell execution, or manifest trust.
package installers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/pterodactyl/wings/internal/sidero/download"
	"github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var (
	ErrManifestInvalid    = errors.New("sidero installers: manifest invalid")
	ErrDestinationInvalid = errors.New("sidero installers: destination invalid")
	ErrDownloadLimit      = errors.New("sidero installers: download limit exceeded")
	ErrConflict           = errors.New("sidero installers: destination conflict")
)

type File struct {
	SourceURL         string `json:"source_url"`
	Destination       string `json:"destination"`
	ExpectedSize      *int64 `json:"expected_size,omitempty"`
	ChecksumAlgorithm string `json:"checksum_algorithm,omitempty"`
	ExpectedChecksum  string `json:"expected_checksum,omitempty"`
}

type Request struct {
	OperationKey   string `json:"operation_key"`
	BackupExisting bool   `json:"backup_existing"`
	ConflictPolicy string `json:"conflict_policy"`
	Files          []File `json:"files"`
}

type Limits struct {
	MaximumFiles         int
	MaximumDownloadBytes int64
}

type FileResult struct {
	Destination string `json:"destination"`
	Bytes       int64  `json:"bytes"`
	Status      string `json:"status"`
	Backup      string `json:"backup,omitempty"`
}

type Result struct {
	OperationID string       `json:"-"`
	Files       []FileResult `json:"files"`
	Bytes       int64        `json:"bytes"`
}

type ProgressFunc func(float64, string)

type entry struct {
	target       string
	stage        string
	rollback     string
	backup       string
	existed      bool
	skipped      bool
	committed    bool
	downloadSize int64
}

func Execute(ctx context.Context, filesystem *wfs.Filesystem, client *download.Client, request Request, limits Limits, progress ProgressFunc) (Result, error) {
	if strings.TrimSpace(request.OperationKey) == "" || len(request.OperationKey) > 256 || len(request.Files) == 0 || len(request.Files) > limits.MaximumFiles {
		return Result{}, ErrManifestInvalid
	}
	if request.ConflictPolicy == "" {
		request.ConflictPolicy = "fail"
	}
	if request.ConflictPolicy != "fail" && request.ConflictPolicy != "skip" && request.ConflictPolicy != "replace" && request.ConflictPolicy != "rename" {
		return Result{}, ErrManifestInvalid
	}

	opID := uuid.NewString()
	stageRoot := path.Join(".sidero", "installers", opID)
	defer func() { _ = filesystem.Delete(stageRoot) }()
	entries := make([]entry, len(request.Files))
	seen := make(map[string]struct{}, len(request.Files))
	var expectedTotal int64
	for i, file := range request.Files {
		target, err := files.NormalizeClientPath(file.Destination)
		if err != nil || target == "." || strings.HasSuffix(file.Destination, "/") {
			return Result{}, ErrDestinationInvalid
		}
		if _, ok := seen[target]; ok {
			return Result{}, ErrManifestInvalid
		}
		seen[target] = struct{}{}
		if strings.TrimSpace(file.SourceURL) == "" {
			return Result{}, ErrManifestInvalid
		}
		if file.ExpectedSize != nil {
			if *file.ExpectedSize < 0 || *file.ExpectedSize > limits.MaximumDownloadBytes-expectedTotal {
				return Result{}, ErrDownloadLimit
			}
			expectedTotal += *file.ExpectedSize
		}
		entries[i] = entry{target: target, stage: path.Join(stageRoot, "payload", strconv.Itoa(i)+".part"), rollback: path.Join(stageRoot, "rollback", strconv.Itoa(i))}
		info, statErr := filesystem.UnixFS().Lstat(target)
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return Result{}, ErrDestinationInvalid
			}
			entries[i].existed = true
			switch request.ConflictPolicy {
			case "fail":
				return Result{}, ErrConflict
			case "skip":
				entries[i].skipped = true
			case "rename":
				renamed, err := nextInstallerName(filesystem, target)
				if err != nil {
					return Result{}, err
				}
				entries[i].target, entries[i].existed = renamed, false
			}
		} else if !errors.Is(statErr, ufs.ErrNotExist) {
			return Result{}, statErr
		}
	}

	result := Result{OperationID: opID, Files: make([]FileResult, len(request.Files))}
	for i, file := range request.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if entries[i].skipped {
			result.Files[i] = FileResult{Destination: entries[i].target, Status: "skipped"}
			continue
		}
		if progress != nil {
			progress(float64(i)/float64(len(request.Files)+1), "Downloading installer files.")
		}
		downloadResult, err := download.Execute(ctx, filesystem, client, download.Request{URL: file.SourceURL, Destination: path.Dir(entries[i].stage), Filename: path.Base(entries[i].stage), ExpectedSize: file.ExpectedSize, ChecksumAlgorithm: file.ChecksumAlgorithm, ExpectedChecksum: file.ExpectedChecksum, ConflictPolicy: download.ConflictFail}, nil)
		if err != nil {
			return Result{}, err
		}
		entries[i].downloadSize = downloadResult.Bytes
		if entries[i].downloadSize > limits.MaximumDownloadBytes-result.Bytes {
			return Result{}, ErrDownloadLimit
		}
		result.Bytes += entries[i].downloadSize
		if request.BackupExisting && entries[i].existed {
			entries[i].backup = path.Join(".sidero", "backups", "installers", opID, entries[i].target)
			if err := copyRegularFile(filesystem, entries[i].target, entries[i].backup); err != nil {
				return Result{}, err
			}
		}
	}

	if progress != nil {
		progress(0.9, "Committing installer files.")
	}
	for i := range entries {
		if entries[i].skipped {
			continue
		}
		if err := ctx.Err(); err != nil {
			rollbackEntries(filesystem, entries)
			return Result{}, err
		}
		if entries[i].existed {
			if err := filesystem.Rename(entries[i].target, entries[i].rollback); err != nil {
				rollbackEntries(filesystem, entries)
				return Result{}, err
			}
		}
		if err := filesystem.Rename(entries[i].stage, entries[i].target); err != nil {
			if entries[i].existed {
				_ = filesystem.Rename(entries[i].rollback, entries[i].target)
			}
			rollbackEntries(filesystem, entries)
			return Result{}, err
		}
		entries[i].committed = true
		result.Files[i] = FileResult{Destination: entries[i].target, Bytes: entries[i].downloadSize, Status: "installed", Backup: entries[i].backup}
	}
	if progress != nil {
		progress(1, "Installer operation completed.")
	}
	return result, nil
}

func rollbackEntries(filesystem *wfs.Filesystem, entries []entry) {
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].committed {
			continue
		}
		_ = filesystem.Delete(entries[i].target)
		if entries[i].existed {
			_ = filesystem.Rename(entries[i].rollback, entries[i].target)
		}
	}
}

func copyRegularFile(filesystem *wfs.Filesystem, source, destination string) error {
	file, stat, err := filesystem.File(source)
	if err != nil {
		return err
	}
	defer file.Close()
	if !stat.Mode().IsRegular() {
		return ErrDestinationInvalid
	}
	return filesystem.Write(destination, io.LimitReader(file, stat.Size()), stat.Size(), stat.Mode().Perm())
}

func nextInstallerName(filesystem *wfs.Filesystem, target string) (string, error) {
	extension := path.Ext(target)
	base := strings.TrimSuffix(target, extension)
	for i := 1; i <= 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, extension)
		if _, err := filesystem.UnixFS().Lstat(candidate); errors.Is(err, ufs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", ErrConflict
}
