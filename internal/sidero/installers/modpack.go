package installers

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	iofs "io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/pterodactyl/wings/internal/sidero/archives"
	"github.com/pterodactyl/wings/internal/sidero/download"
	"github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

type ModpackFile struct {
	URL               string `json:"url"`
	Destination       string `json:"destination"`
	ExpectedSize      *int64 `json:"expected_size,omitempty"`
	ChecksumAlgorithm string `json:"checksum_algorithm,omitempty"`
	Checksum          string `json:"checksum,omitempty"`
}

type OverridesArchive struct {
	URL               string `json:"url"`
	ExpectedSize      *int64 `json:"expected_size,omitempty"`
	ChecksumAlgorithm string `json:"checksum_algorithm,omitempty"`
	Checksum          string `json:"checksum,omitempty"`
}

type ModpackRequest struct {
	OperationKey     string            `json:"operation_key"`
	Mode             string            `json:"mode"`
	CleanInstall     bool              `json:"clean_install"`
	BackupExisting   bool              `json:"backup_existing"`
	Files            []ModpackFile     `json:"files"`
	OverridesArchive *OverridesArchive `json:"overrides_archive,omitempty"`
	RetainedPaths    []string          `json:"retained_paths,omitempty"`
	ConflictPolicy   string            `json:"conflict_policy"`
}

type ModpackResult struct {
	OperationID string `json:"-"`
	Mode        string `json:"mode"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Backup      string `json:"backup,omitempty"`
}

func InstallModpack(ctx context.Context, filesystem *wfs.Filesystem, client *download.Client, request ModpackRequest, limits Limits, archiveLimits archives.Limits, progress ProgressFunc) (ModpackResult, error) {
	if request.OperationKey == "" || len(request.OperationKey) > 256 || request.Mode != "merge" && request.Mode != "clean" || request.Mode == "clean" && !request.CleanInstall || len(request.Files) > limits.MaximumFiles || len(request.Files) == 0 && request.OverridesArchive == nil {
		return ModpackResult{}, ErrManifestInvalid
	}
	if request.ConflictPolicy == "" {
		request.ConflictPolicy = "fail"
	}
	if request.ConflictPolicy != "fail" && request.ConflictPolicy != "replace" && request.ConflictPolicy != "skip" && request.ConflictPolicy != "rename" {
		return ModpackResult{}, ErrManifestInvalid
	}
	if err := validateModpackRequest(ctx, client, request, limits); err != nil {
		return ModpackResult{}, err
	}
	opID := uuid.NewString()
	stageRoot := path.Join(".sidero", "modpacks", opID)
	payload := path.Join(stageRoot, "payload")
	defer func() { _ = filesystem.Delete(stageRoot) }()

	result := ModpackResult{OperationID: opID, Mode: request.Mode}
	if request.OverridesArchive != nil {
		overridePath := path.Join(stageRoot, "overrides.archive")
		dl, err := download.Execute(ctx, filesystem, client, download.Request{URL: request.OverridesArchive.URL, Destination: path.Dir(overridePath), Filename: path.Base(overridePath), ExpectedSize: request.OverridesArchive.ExpectedSize, ChecksumAlgorithm: request.OverridesArchive.ChecksumAlgorithm, ExpectedChecksum: request.OverridesArchive.Checksum, ConflictPolicy: download.ConflictFail}, nil)
		if err != nil {
			return ModpackResult{}, err
		}
		result.Bytes += dl.Bytes
		if result.Bytes > limits.MaximumDownloadBytes {
			return ModpackResult{}, ErrDownloadLimit
		}
		extracted, err := archives.Extract(ctx, filesystem, archives.ExtractRequest{Archive: overridePath, Destination: payload, ConflictPolicy: "fail"}, archiveLimits, nil)
		if err != nil {
			return ModpackResult{}, err
		}
		result.Files += extracted.FileCount
	} else if err := filesystem.CreateDirectory(path.Base(payload), path.Dir(payload)); err != nil {
		return ModpackResult{}, err
	}

	seen := map[string]struct{}{}
	for i, file := range request.Files {
		if err := ctx.Err(); err != nil {
			return ModpackResult{}, err
		}
		destination, err := files.NormalizeClientPath(file.Destination)
		if err != nil || destination == "." {
			return ModpackResult{}, ErrDestinationInvalid
		}
		if _, exists := seen[destination]; exists {
			return ModpackResult{}, ErrManifestInvalid
		}
		seen[destination] = struct{}{}
		if progress != nil {
			progress(float64(i)/float64(len(request.Files)+2), "Downloading resolved modpack files.")
		}
		staged := path.Join(payload, destination)
		dl, err := download.Execute(ctx, filesystem, client, download.Request{URL: file.URL, Destination: path.Dir(staged), Filename: path.Base(staged), ExpectedSize: file.ExpectedSize, ChecksumAlgorithm: file.ChecksumAlgorithm, ExpectedChecksum: file.Checksum, ConflictPolicy: download.ConflictReplace}, nil)
		if err != nil {
			return ModpackResult{}, err
		}
		result.Bytes += dl.Bytes
		if result.Bytes > limits.MaximumDownloadBytes {
			return ModpackResult{}, ErrDownloadLimit
		}
		result.Files++
	}

	if request.Mode == "clean" {
		for _, retained := range request.RetainedPaths {
			rel, err := files.NormalizeClientPath(retained)
			if err != nil || rel == "." || rel == ".sidero" || strings.HasPrefix(rel, ".sidero/") {
				return ModpackResult{}, ErrDestinationInvalid
			}
			if err := copyTree(ctx, filesystem, rel, path.Join(payload, rel), true); err != nil && !errors.Is(err, ufs.ErrNotExist) {
				return ModpackResult{}, err
			}
		}
		backup, err := commitCleanModpack(ctx, filesystem, payload, stageRoot, opID, request.BackupExisting)
		if err != nil {
			return ModpackResult{}, err
		}
		result.Backup = backup
	} else if err := commitMergeModpack(ctx, filesystem, payload, stageRoot, opID, request.ConflictPolicy, request.BackupExisting); err != nil {
		return ModpackResult{}, err
	}
	if progress != nil {
		progress(1, "Modpack installation completed.")
	}
	return result, nil
}

func validateModpackRequest(ctx context.Context, client *download.Client, request ModpackRequest, limits Limits) error {
	seen := make(map[string]struct{}, len(request.Files))
	var expectedTotal int64
	validateArtifact := func(url string, expectedSize *int64, algorithm, checksum string) error {
		if _, err := client.ValidateURL(ctx, url); err != nil {
			return err
		}
		if expectedSize == nil {
			return ErrManifestInvalid
		}
		if *expectedSize < 0 || *expectedSize > limits.MaximumDownloadBytes-expectedTotal {
			return ErrDownloadLimit
		}
		if !validModpackChecksum(algorithm, checksum) {
			return ErrManifestInvalid
		}
		expectedTotal += *expectedSize
		return nil
	}
	if request.OverridesArchive != nil {
		if err := validateArtifact(request.OverridesArchive.URL, request.OverridesArchive.ExpectedSize, request.OverridesArchive.ChecksumAlgorithm, request.OverridesArchive.Checksum); err != nil {
			return err
		}
	}
	for _, file := range request.Files {
		destination, err := files.NormalizeClientPath(file.Destination)
		if err != nil || destination == "." || strings.HasSuffix(file.Destination, "/") {
			return ErrDestinationInvalid
		}
		if _, exists := seen[destination]; exists {
			return ErrManifestInvalid
		}
		seen[destination] = struct{}{}
		if err := validateArtifact(file.URL, file.ExpectedSize, file.ChecksumAlgorithm, file.Checksum); err != nil {
			return err
		}
	}
	for _, retained := range request.RetainedPaths {
		rel, err := files.NormalizeClientPath(retained)
		if err != nil || rel == "." || rel == ".sidero" || strings.HasPrefix(rel, ".sidero/") {
			return ErrDestinationInvalid
		}
	}
	return nil
}

func validModpackChecksum(algorithm, checksum string) bool {
	algorithm = strings.ToLower(strings.TrimSpace(algorithm))
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return false
	}
	length := 0
	switch algorithm {
	case "sha1":
		length = sha1.Size
	case "sha256":
		length = sha256.Size
	case "sha512":
		length = sha512.Size
	default:
		return false
	}
	decoded, err := hex.DecodeString(checksum)
	return err == nil && len(decoded) == length
}

type treeEntry struct {
	rel  string
	dir  bool
	size int64
}

func listTree(ctx context.Context, filesystem *wfs.Filesystem, root string) ([]treeEntry, error) {
	var out []treeEntry
	var walk func(string, string) error
	walk = func(current, relative string) error {
		entries, err := filesystem.ReadDir(current)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			source := path.Join(current, entry.Name())
			rel := path.Join(relative, entry.Name())
			info, err := filesystem.UnixFS().Lstat(source)
			if err != nil {
				return err
			}
			if info.Mode()&iofs.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return ErrDestinationInvalid
			}
			out = append(out, treeEntry{rel: rel, dir: info.IsDir(), size: info.Size()})
			if info.IsDir() {
				if err := walk(source, rel); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, "."); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func copyTree(ctx context.Context, filesystem *wfs.Filesystem, source, destination string, replace bool) error {
	info, err := filesystem.UnixFS().Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&iofs.ModeSymlink != 0 {
		return ErrDestinationInvalid
	}
	if info.Mode().IsRegular() {
		if replace {
			_ = filesystem.Delete(destination)
		}
		return copyRegularFile(filesystem, source, destination)
	}
	if !info.IsDir() {
		return ErrDestinationInvalid
	}
	if err := filesystem.CreateDirectory(path.Base(destination), path.Dir(destination)); err != nil && !errors.Is(err, ufs.ErrExist) {
		return err
	}
	entries, err := filesystem.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyTree(ctx, filesystem, path.Join(source, entry.Name()), path.Join(destination, entry.Name()), replace); err != nil {
			return err
		}
	}
	return nil
}

func commitMergeModpack(ctx context.Context, filesystem *wfs.Filesystem, payload, stageRoot, opID, conflictPolicy string, backupExisting bool) error {
	entries, err := listTree(ctx, filesystem, payload)
	if err != nil {
		return err
	}
	type commitEntry struct {
		source   string
		target   string
		existing bool
	}
	planned := make([]commitEntry, 0, len(entries))
	reserved := map[string]struct{}{}
	for _, entry := range entries {
		if entry.dir {
			continue
		}
		target := entry.rel
		source := path.Join(payload, entry.rel)
		info, statErr := filesystem.UnixFS().Lstat(target)
		existing := statErr == nil
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return ErrDestinationInvalid
			}
			switch conflictPolicy {
			case "fail":
				return ErrConflict
			case "skip":
				continue
			case "rename":
				target, err = nextModpackName(filesystem, target, reserved)
				if err != nil {
					return err
				}
				existing = false
			}
		} else if !errors.Is(statErr, ufs.ErrNotExist) {
			return statErr
		}
		if _, duplicate := reserved[target]; duplicate {
			return ErrManifestInvalid
		}
		reserved[target] = struct{}{}
		planned = append(planned, commitEntry{source: source, target: target, existing: existing})
	}
	for _, entry := range planned {
		if backupExisting && entry.existing {
			if err := copyRegularFile(filesystem, entry.target, path.Join(".sidero", "backups", "modpacks", opID, entry.target)); err != nil {
				return err
			}
		}
	}
	committed := make([]commitEntry, 0, len(planned))
	rollback := func() error {
		var rollbackErr error
		for i := len(committed) - 1; i >= 0; i-- {
			entry := committed[i]
			if err := filesystem.Rename(entry.target, entry.source); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
			if entry.existing {
				if err := filesystem.Rename(path.Join(stageRoot, "rollback", entry.target), entry.target); err != nil {
					rollbackErr = errors.Join(rollbackErr, err)
				}
			}
		}
		return rollbackErr
	}
	for _, entry := range planned {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollback())
		}
		if entry.existing {
			previous := path.Join(stageRoot, "rollback", entry.target)
			if err := filesystem.Rename(entry.target, previous); err != nil {
				return errors.Join(err, rollback())
			}
		}
		if err := filesystem.Rename(entry.source, entry.target); err != nil {
			if entry.existing {
				_ = filesystem.Rename(path.Join(stageRoot, "rollback", entry.target), entry.target)
			}
			return errors.Join(err, rollback())
		}
		committed = append(committed, entry)
	}
	return nil
}

func nextModpackName(filesystem *wfs.Filesystem, target string, reserved map[string]struct{}) (string, error) {
	for i := 1; i <= 1000; i++ {
		candidate := target + " (" + strconv.Itoa(i) + ")"
		if _, exists := reserved[candidate]; exists {
			continue
		}
		if _, err := filesystem.UnixFS().Lstat(candidate); errors.Is(err, ufs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", ErrConflict
}

func commitCleanModpack(ctx context.Context, filesystem *wfs.Filesystem, payload, stageRoot, opID string, backupExisting bool) (string, error) {
	current, err := filesystem.ReadDir(".")
	if err != nil {
		return "", err
	}
	staged, err := filesystem.ReadDir(payload)
	if err != nil {
		return "", err
	}
	rollback := path.Join(stageRoot, "rollback-current")
	movedOld := make([]string, 0)
	restoreOld := func() error {
		var restoreErr error
		for i := len(movedOld) - 1; i >= 0; i-- {
			if err := filesystem.Rename(path.Join(rollback, movedOld[i]), movedOld[i]); err != nil {
				restoreErr = errors.Join(restoreErr, err)
			}
		}
		return restoreErr
	}
	for _, entry := range current {
		if err := ctx.Err(); err != nil {
			return "", errors.Join(err, restoreOld())
		}
		if entry.Name() == ".sidero" {
			continue
		}
		if err := filesystem.Rename(entry.Name(), path.Join(rollback, entry.Name())); err != nil {
			return "", errors.Join(err, restoreOld())
		}
		movedOld = append(movedOld, entry.Name())
	}
	movedNew := make([]string, 0)
	restoreNew := func() error {
		var restoreErr error
		for i := len(movedNew) - 1; i >= 0; i-- {
			if err := filesystem.Rename(movedNew[i], path.Join(payload, movedNew[i])); err != nil {
				restoreErr = errors.Join(restoreErr, err)
			}
		}
		return restoreErr
	}
	for _, entry := range staged {
		if err := ctx.Err(); err != nil {
			return "", errors.Join(err, restoreNew(), restoreOld())
		}
		if err := filesystem.Rename(path.Join(payload, entry.Name()), entry.Name()); err != nil {
			return "", errors.Join(err, restoreNew(), restoreOld())
		}
		movedNew = append(movedNew, entry.Name())
	}
	if backupExisting && len(movedOld) > 0 {
		backup := path.Join(".sidero", "backups", "modpacks", opID)
		if err := filesystem.Rename(rollback, backup); err != nil {
			return "", errors.Join(err, restoreNew(), restoreOld())
		}
		return backup, nil
	}
	return "", nil
}
