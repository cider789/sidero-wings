// Package worlds implements server-root-scoped Minecraft world filesystem operations.
package worlds

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"path"
	"strconv"

	"github.com/google/uuid"

	"github.com/pterodactyl/wings/internal/sidero/archives"
	"github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var (
	ErrInvalidWorld  = errors.New("sidero worlds: invalid world")
	ErrServerRunning = errors.New("sidero worlds: server is running")
	ErrSizeLimit     = errors.New("sidero worlds: size limit exceeded")
	ErrConflict      = errors.New("sidero worlds: destination conflict")
)

type Inspection struct {
	Path   string `json:"path"`
	Valid  bool   `json:"valid"`
	Format string `json:"format,omitempty"`
	Size   int64  `json:"size"`
	Files  int    `json:"files"`
}

type Result struct {
	Path   string `json:"path"`
	Backup string `json:"backup,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type ProgressFunc func(float64, string)

func Inspect(ctx context.Context, filesystem *wfs.Filesystem, worldPath string, maximumBytes int64) (Inspection, error) {
	rel, err := files.NormalizeRelativePath(worldPath)
	if err != nil || rel == "." {
		return Inspection{}, ErrInvalidWorld
	}
	info, err := filesystem.UnixFS().Lstat(rel)
	if err != nil || !info.IsDir() || info.Mode()&iofs.ModeSymlink != 0 {
		return Inspection{}, ErrInvalidWorld
	}
	level, err := filesystem.UnixFS().Lstat(path.Join(rel, "level.dat"))
	if err != nil || !level.Mode().IsRegular() {
		return Inspection{}, ErrInvalidWorld
	}
	format := "java"
	if database, err := filesystem.UnixFS().Lstat(path.Join(rel, "db")); err == nil && database.IsDir() {
		format = "bedrock"
	}
	size, count, err := calculateSize(ctx, filesystem, rel, maximumBytes)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{Path: rel, Valid: true, Format: format, Size: size, Files: count}, nil
}

func Size(ctx context.Context, filesystem *wfs.Filesystem, worldPath string, maximumBytes int64) (Result, error) {
	inspection, err := Inspect(ctx, filesystem, worldPath, maximumBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Path: inspection.Path, Size: inspection.Size}, nil
}

func Clone(ctx context.Context, filesystem *wfs.Filesystem, source, destination string, maximumBytes int64, serverRunning bool, progress ProgressFunc) (Result, error) {
	if serverRunning {
		return Result{}, ErrServerRunning
	}
	inspection, err := Inspect(ctx, filesystem, source, maximumBytes)
	if err != nil {
		return Result{}, err
	}
	if err := filesystem.HasSpaceFor(inspection.Size); err != nil {
		return Result{}, err
	}
	destination, err = normalizeDestination(filesystem, destination)
	if err != nil {
		return Result{}, err
	}
	opID := uuid.NewString()
	stageRoot := path.Join(".sidero", "worlds", opID)
	payload := path.Join(stageRoot, "payload")
	defer func() { _ = filesystem.Delete(stageRoot) }()
	if progress != nil {
		progress(0.05, "Cloning world into staging.")
	}
	if err := copyWorldTree(ctx, filesystem, inspection.Path, payload, maximumBytes, nil); err != nil {
		return Result{}, err
	}
	if _, err := Inspect(ctx, filesystem, payload, maximumBytes); err != nil {
		return Result{}, err
	}
	if err := filesystem.Rename(payload, destination); err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(1, "World clone completed.")
	}
	return Result{Path: destination, Size: inspection.Size}, nil
}

func Import(ctx context.Context, filesystem *wfs.Filesystem, archivePath, destination, conflictPolicy string, maximumBytes int64, serverRunning bool, limits archives.Limits, progress ProgressFunc) (Result, error) {
	if serverRunning {
		return Result{}, ErrServerRunning
	}
	destination, err := files.NormalizeRelativePath(destination)
	if err != nil || destination == "." || destination == ".sidero" || pathHasPrefix(destination, ".sidero") {
		return Result{}, ErrConflict
	}
	if conflictPolicy == "" {
		conflictPolicy = "fail"
	}
	if conflictPolicy != "fail" && conflictPolicy != "skip" && conflictPolicy != "replace" && conflictPolicy != "rename" {
		return Result{}, ErrConflict
	}
	opID := uuid.NewString()
	stageRoot := path.Join(".sidero", "worlds", opID)
	stagedWorld := path.Join(stageRoot, "payload")
	defer func() { _ = filesystem.Delete(stageRoot) }()
	result, err := archives.Extract(ctx, filesystem, archives.ExtractRequest{Archive: archivePath, Destination: stagedWorld, ConflictPolicy: "fail"}, limits, func(value float64, message string) {
		if progress != nil {
			progress(value*0.9, message)
		}
	})
	if err != nil {
		return Result{}, err
	}
	inspection, err := Inspect(ctx, filesystem, result.Destination, maximumBytes)
	if err != nil {
		return Result{}, ErrInvalidWorld
	}
	committed, err := commitImportedWorld(filesystem, result.Destination, destination, stageRoot, conflictPolicy)
	if err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(1, "World import completed.")
	}
	return Result{Path: committed, Size: inspection.Size}, nil
}

func commitImportedWorld(filesystem *wfs.Filesystem, staged, destination, stageRoot, policy string) (string, error) {
	_, err := filesystem.UnixFS().Lstat(destination)
	if errors.Is(err, ufs.ErrNotExist) {
		return destination, filesystem.Rename(staged, destination)
	}
	if err != nil {
		return "", err
	}
	switch policy {
	case "fail":
		return "", ErrConflict
	case "skip":
		if _, err := Inspect(context.Background(), filesystem, destination, 1<<63-1); err != nil {
			return "", ErrConflict
		}
		return destination, nil
	case "rename":
		for i := 1; i <= 1000; i++ {
			candidate := destination + " (" + strconv.Itoa(i) + ")"
			if _, err := filesystem.UnixFS().Lstat(candidate); errors.Is(err, ufs.ErrNotExist) {
				return candidate, filesystem.Rename(staged, candidate)
			} else if err != nil {
				return "", err
			}
		}
		return "", ErrConflict
	case "replace":
		previous := path.Join(stageRoot, "previous")
		if err := filesystem.Rename(destination, previous); err != nil {
			return "", err
		}
		if err := filesystem.Rename(staged, destination); err != nil {
			if rollbackErr := filesystem.Rename(previous, destination); rollbackErr != nil {
				return "", errors.Join(err, rollbackErr)
			}
			return "", err
		}
		return destination, nil
	default:
		return "", ErrConflict
	}
}

func pathHasPrefix(value, prefix string) bool {
	return value == prefix || len(value) > len(prefix) && value[:len(prefix)+1] == prefix+"/"
}

func Archive(ctx context.Context, filesystem *wfs.Filesystem, source, destination string, maximumBytes int64, progress ProgressFunc) (Result, error) {
	inspection, err := Inspect(ctx, filesystem, source, maximumBytes)
	if err != nil {
		return Result{}, err
	}
	if err := filesystem.HasSpaceFor(inspection.Size); err != nil {
		return Result{}, err
	}
	destination, err = files.NormalizeRelativePath(destination)
	if err != nil || destination == "." {
		return Result{}, ErrConflict
	}
	if _, err := filesystem.UnixFS().Lstat(destination); err == nil {
		return Result{}, ErrConflict
	} else if !errors.Is(err, ufs.ErrNotExist) {
		return Result{}, err
	}
	opID := uuid.NewString()
	stageRoot := path.Join(".sidero", "worlds", opID)
	stage := path.Join(stageRoot, "world.tar.gz.part")
	defer func() { _ = filesystem.Delete(stageRoot) }()
	output, err := filesystem.Touch(stage, ufs.O_WRONLY|ufs.O_CREATE|ufs.O_EXCL)
	if err != nil {
		return Result{}, err
	}
	archive := &wfs.Archive{Filesystem: filesystem, BaseDirectory: inspection.Path}
	if err := archive.Stream(ctx, output); err != nil {
		_ = output.Close()
		return Result{}, err
	}
	if err := output.Close(); err != nil {
		return Result{}, err
	}
	if err := filesystem.Chown(stage); err != nil {
		return Result{}, err
	}
	stat, err := filesystem.Stat(stage)
	if err != nil {
		return Result{}, err
	}
	if err := filesystem.Rename(stage, destination); err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(1, "World archive completed.")
	}
	return Result{Path: destination, Size: stat.Size()}, nil
}

func Rename(filesystem *wfs.Filesystem, source, destination string, serverRunning bool) (Result, error) {
	if serverRunning {
		return Result{}, ErrServerRunning
	}
	if _, err := Inspect(context.Background(), filesystem, source, 1<<63-1); err != nil {
		return Result{}, err
	}
	destination, err := normalizeDestination(filesystem, destination)
	if err != nil {
		return Result{}, err
	}
	if err := filesystem.Rename(source, destination); err != nil {
		return Result{}, err
	}
	return Result{Path: destination}, nil
}

func Replace(ctx context.Context, filesystem *wfs.Filesystem, source, destination string, backupExisting, serverRunning bool) (Result, error) {
	if serverRunning {
		return Result{}, ErrServerRunning
	}
	sourceInspection, err := Inspect(ctx, filesystem, source, 1<<63-1)
	if err != nil {
		return Result{}, err
	}
	destination, err = files.NormalizeRelativePath(destination)
	if err != nil || destination == "." || sourceInspection.Path == destination {
		return Result{}, ErrConflict
	}
	if _, err := Inspect(ctx, filesystem, destination, 1<<63-1); err != nil {
		return Result{}, err
	}
	opID := uuid.NewString()
	rollback := path.Join(".sidero", "worlds", opID, "previous")
	if err := filesystem.Rename(destination, rollback); err != nil {
		return Result{}, err
	}
	if err := filesystem.Rename(sourceInspection.Path, destination); err != nil {
		_ = filesystem.Rename(rollback, destination)
		return Result{}, err
	}
	backup := ""
	if backupExisting {
		backup = path.Join(".sidero", "backups", "worlds", opID)
		if err := filesystem.Rename(rollback, backup); err != nil {
			_ = filesystem.Rename(destination, sourceInspection.Path)
			_ = filesystem.Rename(rollback, destination)
			return Result{}, err
		}
	} else {
		_ = filesystem.Delete(rollback)
	}
	return Result{Path: destination, Backup: backup, Size: sourceInspection.Size}, nil
}

func normalizeDestination(filesystem *wfs.Filesystem, destination string) (string, error) {
	rel, err := files.NormalizeRelativePath(destination)
	if err != nil || rel == "." || rel == ".sidero" || len(rel) > 4096 {
		return "", ErrConflict
	}
	if _, err := filesystem.UnixFS().Lstat(rel); err == nil {
		return "", ErrConflict
	} else if !errors.Is(err, ufs.ErrNotExist) {
		return "", err
	}
	return rel, nil
}

func calculateSize(ctx context.Context, filesystem *wfs.Filesystem, root string, maximum int64) (int64, int, error) {
	var total int64
	var count int
	var walk func(string) error
	walk = func(current string) error {
		entries, err := filesystem.ReadDir(current)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := path.Join(current, entry.Name())
			info, err := filesystem.UnixFS().Lstat(child)
			if err != nil {
				return err
			}
			if info.Mode()&iofs.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return ErrInvalidWorld
			}
			if info.IsDir() {
				if err := walk(child); err != nil {
					return err
				}
			} else {
				total += info.Size()
				count++
				if total > maximum {
					return ErrSizeLimit
				}
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return 0, 0, err
	}
	return total, count, nil
}

func copyWorldTree(ctx context.Context, filesystem *wfs.Filesystem, source, destination string, maximum int64, copied *int64) error {
	if copied == nil {
		copied = new(int64)
	}
	info, err := filesystem.UnixFS().Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&iofs.ModeSymlink != 0 {
		return ErrInvalidWorld
	}
	if info.Mode().IsRegular() {
		*copied += info.Size()
		if *copied > maximum {
			return ErrSizeLimit
		}
		input, _, err := filesystem.File(source)
		if err != nil {
			return err
		}
		defer input.Close()
		return filesystem.Write(destination, io.LimitReader(input, info.Size()), info.Size(), info.Mode().Perm())
	}
	if !info.IsDir() {
		return ErrInvalidWorld
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
		if err := copyWorldTree(ctx, filesystem, path.Join(source, entry.Name()), path.Join(destination, entry.Name()), maximum, copied); err != nil {
			return err
		}
	}
	return nil
}
