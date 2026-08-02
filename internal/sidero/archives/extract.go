package archives

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
	archivelib "github.com/mholt/archives"

	siderofiles "github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var (
	ErrTraversal   = errors.New("sidero archives: traversal detected")
	ErrUnsafeEntry = errors.New("sidero archives: unsafe entry")
	ErrEntryLimit  = errors.New("sidero archives: entry limit exceeded")
	ErrSizeLimit   = errors.New("sidero archives: size limit exceeded")
	ErrRatioLimit  = errors.New("sidero archives: ratio limit exceeded")
	ErrConflict    = errors.New("sidero archives: destination conflict")
)

type ExtractRequest struct {
	Archive        string `json:"archive"`
	Destination    string `json:"destination"`
	ConflictPolicy string `json:"conflict_policy"`
}

type ExtractResult struct {
	OperationID       string `json:"-"`
	Destination       string `json:"destination"`
	EntryCount        int    `json:"entry_count"`
	FileCount         int    `json:"file_count"`
	DirectoryCount    int    `json:"directory_count"`
	UncompressedBytes int64  `json:"uncompressed_bytes"`
	Skipped           bool   `json:"skipped,omitempty"`
}

type ExtractProgressFunc func(float64, string)

func Extract(ctx context.Context, filesystem *wfs.Filesystem, request ExtractRequest, limits Limits, progress ExtractProgressFunc) (ExtractResult, error) {
	archivePath, err := siderofiles.NormalizeRelativePath(request.Archive)
	if err != nil || archivePath == "." {
		return ExtractResult{}, ErrTraversal
	}
	destination, err := siderofiles.NormalizeRelativePath(request.Destination)
	if err != nil || destination == "." || destination == archivePath || strings.HasPrefix(archivePath, destination+"/") {
		return ExtractResult{}, ErrTraversal
	}
	if request.ConflictPolicy == "" {
		request.ConflictPolicy = "fail"
	}
	if request.ConflictPolicy != "fail" && request.ConflictPolicy != "skip" && request.ConflictPolicy != "replace" && request.ConflictPolicy != "rename" {
		return ExtractResult{}, ErrConflict
	}
	if progress != nil {
		progress(0.01, "Inspecting archive before extraction.")
	}
	inspection, err := Inspect(ctx, filesystem, archivePath, limits)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := filesystem.HasSpaceFor(inspection.DiskSpaceEstimate); err != nil {
		return ExtractResult{}, err
	}

	archiveFile, archiveStat, err := filesystem.File(archivePath)
	if err != nil {
		return ExtractResult{}, err
	}
	defer archiveFile.Close()
	format, reader, err := archivelib.Identify(ctx, path.Base(archivePath), archiveFile)
	if err != nil {
		return ExtractResult{}, ErrMalformed
	}
	extractor, ok := format.(archivelib.Extractor)
	if !ok {
		return ExtractResult{}, ErrMalformed
	}

	opID := uuid.NewString()
	stageRoot := path.Join(".sidero", "archives", opID)
	payload := path.Join(stageRoot, "payload")
	defer func() { _ = filesystem.Delete(stageRoot) }()
	if err := filesystem.CreateDirectory("payload", stageRoot); err != nil {
		return ExtractResult{}, err
	}

	result := ExtractResult{OperationID: opID, Destination: destination}
	seen := make(map[string]struct{})
	if progress != nil {
		progress(0.02, "Extracting archive into staging.")
	}
	err = extractor.Extract(ctx, reader, func(ctx context.Context, item archivelib.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := safeEntryPath(item.NameInArchive)
		if err != nil {
			return err
		}
		if entry == "" {
			if item.IsDir() {
				return nil
			}
			return ErrTraversal
		}
		if _, exists := seen[entry]; exists {
			return ErrUnsafeEntry
		}
		seen[entry] = struct{}{}
		result.EntryCount++
		if result.EntryCount > limits.MaximumEntries {
			return ErrEntryLimit
		}
		if item.Size() < 0 || item.Size() > limits.MaximumSingleEntryBytes {
			return ErrSizeLimit
		}
		if item.Mode()&(iofs.ModeSymlink|iofs.ModeDevice|iofs.ModeNamedPipe|iofs.ModeSocket|iofs.ModeCharDevice|iofs.ModeIrregular) != 0 {
			return ErrUnsafeEntry
		}
		if header, ok := item.Header.(*tar.Header); ok && (header.Typeflag == tar.TypeLink || header.Typeflag == tar.TypeSymlink) {
			return ErrUnsafeEntry
		}
		result.UncompressedBytes += item.Size()
		if result.UncompressedBytes > limits.MaximumUncompressedBytes {
			return ErrSizeLimit
		}
		if archiveStat.Size() > 0 && float64(result.UncompressedBytes)/float64(archiveStat.Size()) > limits.MaximumCompressionRatio {
			return ErrRatioLimit
		}
		output := path.Join(payload, entry)
		if item.IsDir() {
			result.DirectoryCount++
			return filesystem.CreateDirectory(path.Base(output), path.Dir(output))
		}
		if !item.Mode().IsRegular() {
			return ErrUnsafeEntry
		}
		input, err := item.Open()
		if err != nil {
			return err
		}
		defer input.Close()
		mode := item.Mode().Perm() & 0o755
		if mode == 0 {
			mode = 0o644
		}
		if err := filesystem.Write(output, &exactReader{reader: input, remaining: item.Size()}, item.Size(), mode); err != nil {
			return err
		}
		result.FileCount++
		if progress != nil {
			progress(min(0.9, float64(result.UncompressedBytes)/float64(max(limits.MaximumUncompressedBytes, 1))), "Extracting archive into staging.")
		}
		return nil
	})
	if err != nil {
		return ExtractResult{}, unwrapExtractionError(err)
	}
	if err := ctx.Err(); err != nil {
		return ExtractResult{}, err
	}
	if progress != nil {
		progress(0.95, "Committing extracted files.")
	}
	finalDestination, skipped, err := commitExtracted(filesystem, payload, destination, stageRoot, request.ConflictPolicy)
	if err != nil {
		return ExtractResult{}, err
	}
	result.Destination = finalDestination
	result.Skipped = skipped
	if progress != nil {
		progress(1, "Archive extraction completed.")
	}
	return result, nil
}

func safeEntryPath(value string) (string, error) {
	if value == "" || len(value) > 4096 || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || windowsDrive.MatchString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", ErrTraversal
	}
	clean := path.Clean(value)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrTraversal
	}
	return clean, nil
}

func commitExtracted(filesystem *wfs.Filesystem, payload, destination, stageRoot, policy string) (string, bool, error) {
	_, err := filesystem.UnixFS().Lstat(destination)
	if errors.Is(err, ufs.ErrNotExist) {
		return destination, false, filesystem.Rename(payload, destination)
	}
	if err != nil {
		return "", false, err
	}
	switch policy {
	case "fail":
		return "", false, ErrConflict
	case "skip":
		return destination, true, nil
	case "rename":
		for i := 1; i <= 1000; i++ {
			candidate := destination + " (" + strconv.Itoa(i) + ")"
			if _, err := filesystem.UnixFS().Lstat(candidate); errors.Is(err, ufs.ErrNotExist) {
				return candidate, false, filesystem.Rename(payload, candidate)
			} else if err != nil {
				return "", false, err
			}
		}
		return "", false, ErrConflict
	case "replace":
		previous := path.Join(stageRoot, "previous")
		if err := filesystem.Rename(destination, previous); err != nil {
			return "", false, err
		}
		if err := filesystem.Rename(payload, destination); err != nil {
			rollbackErr := filesystem.Rename(previous, destination)
			if rollbackErr != nil {
				return "", false, errors.Join(err, rollbackErr)
			}
			return "", false, err
		}
		if err := filesystem.Delete(previous); err != nil {
			return "", false, err
		}
		return destination, false, nil
	default:
		return "", false, ErrConflict
	}
}

type exactReader struct {
	reader    io.Reader
	remaining int64
}

func (r *exactReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	if err == io.EOF && r.remaining > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func unwrapExtractionError(err error) error {
	for _, target := range []error{ErrTraversal, ErrUnsafeEntry, ErrEntryLimit, ErrSizeLimit, ErrRatioLimit, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, target) {
			return target
		}
	}
	return ErrMalformed
}
