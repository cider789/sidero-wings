// Package archives adds security preflight to Wings' existing archive stack.
package archives

import (
	"archive/tar"
	"context"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	archivelib "github.com/mholt/archives"

	siderofiles "github.com/pterodactyl/wings/internal/sidero/files"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var ErrMalformed = errors.New("sidero archives: malformed or unsupported archive")
var windowsDrive = regexp.MustCompile(`^[A-Za-z]:`)

type Limits struct {
	MaximumEntries           int
	MaximumUncompressedBytes int64
	MaximumSingleEntryBytes  int64
	MaximumCompressionRatio  float64
	AllowSymbolicLinks       bool
}

type LargestEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
type SuspiciousEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type Report struct {
	Format                     string            `json:"format"`
	EntryCount                 int               `json:"entry_count"`
	FileCount                  int               `json:"file_count"`
	DirectoryCount             int               `json:"directory_count"`
	SymbolicLinkCount          int               `json:"symbolic_link_count"`
	EstimatedUncompressedBytes int64             `json:"estimated_uncompressed_bytes"`
	LargestEntry               LargestEntry      `json:"largest_entry"`
	MaximumPathDepth           int               `json:"maximum_path_depth"`
	SuspiciousEntries          []SuspiciousEntry `json:"suspicious_entries"`
	Unsafe                     bool              `json:"unsafe"`
	RejectionReasons           []string          `json:"rejection_reasons"`
	DiskSpaceEstimate          int64             `json:"disk_space_estimate"`
}

func Inspect(ctx context.Context, filesystem *wfs.Filesystem, archivePath string, limits Limits) (Report, error) {
	rel, err := siderofiles.NormalizeRelativePath(archivePath)
	if err != nil {
		return Report{}, err
	}
	file, stat, err := filesystem.File(rel)
	if err != nil {
		return Report{}, err
	}
	defer file.Close()
	format, reader, err := archivelib.Identify(ctx, path.Base(rel), file)
	if err != nil {
		return Report{}, ErrMalformed
	}
	extractor, ok := format.(archivelib.Extractor)
	if !ok {
		return Report{}, ErrMalformed
	}
	report := Report{Format: format.Extension(), SuspiciousEntries: make([]SuspiciousEntry, 0)}
	reasons := map[string]struct{}{}
	addReason := func(code string) { reasons[code] = struct{}{}; report.Unsafe = true }
	err = extractor.Extract(ctx, reader, func(ctx context.Context, item archivelib.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		report.EntryCount++
		name := item.NameInArchive
		clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
		unsafePath := name == "" || len(name) > 4096 || strings.IndexByte(name, 0) >= 0 || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || windowsDrive.MatchString(name) || clean == ".." || strings.HasPrefix(clean, "../")
		if unsafePath {
			addReason("archive_traversal_detected")
			appendSuspicious(&report, name, "archive_traversal_detected")
		}
		depth := 0
		if clean != "." {
			depth = len(strings.Split(strings.Trim(clean, "/"), "/"))
		}
		if depth > report.MaximumPathDepth {
			report.MaximumPathDepth = depth
		}
		size := item.Size()
		if size < 0 {
			addReason("archive_malformed")
			size = 0
		}
		report.EstimatedUncompressedBytes += size
		if size > report.LargestEntry.Size {
			report.LargestEntry = LargestEntry{Path: safeArchivePath(name), Size: size}
		}
		mode := item.Mode()
		if item.IsDir() {
			report.DirectoryCount++
		} else {
			report.FileCount++
		}
		if mode&fs.ModeSymlink != 0 {
			report.SymbolicLinkCount++
			if !limits.AllowSymbolicLinks || unsafeLink(name, item.LinkTarget) {
				addReason("unsafe_symbolic_link")
				appendSuspicious(&report, name, "unsafe_symbolic_link")
			}
		}
		if header, ok := item.Header.(*tar.Header); ok && header.Typeflag == tar.TypeLink {
			addReason("archive_unsafe_entry_type")
			appendSuspicious(&report, name, "archive_unsafe_entry_type")
			if unsafeLink(name, header.Linkname) {
				addReason("archive_traversal_detected")
				appendSuspicious(&report, name, "unsafe_hard_link")
			}
		}
		if mode&(fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeCharDevice) != 0 {
			addReason("archive_unsafe_entry_type")
			appendSuspicious(&report, name, "archive_unsafe_entry_type")
		}
		if report.EntryCount > limits.MaximumEntries {
			addReason("archive_entry_limit_exceeded")
		}
		if size > limits.MaximumSingleEntryBytes {
			addReason("archive_size_limit_exceeded")
		}
		if report.EstimatedUncompressedBytes > limits.MaximumUncompressedBytes {
			addReason("archive_size_limit_exceeded")
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return Report{}, ctx.Err()
		}
		return Report{}, ErrMalformed
	}
	if stat.Size() > 0 && float64(report.EstimatedUncompressedBytes)/float64(stat.Size()) > limits.MaximumCompressionRatio {
		addReason("archive_ratio_limit_exceeded")
	}
	report.DiskSpaceEstimate = report.EstimatedUncompressedBytes
	for reason := range reasons {
		report.RejectionReasons = append(report.RejectionReasons, reason)
	}
	sort.Strings(report.RejectionReasons)
	sort.Slice(report.SuspiciousEntries, func(i, j int) bool {
		if report.SuspiciousEntries[i].Path == report.SuspiciousEntries[j].Path {
			return report.SuspiciousEntries[i].Reason < report.SuspiciousEntries[j].Reason
		}
		return report.SuspiciousEntries[i].Path < report.SuspiciousEntries[j].Path
	})
	return report, nil
}

func unsafeLink(name, target string) bool {
	if target == "" || strings.HasPrefix(target, "/") || windowsDrive.MatchString(target) || strings.Contains(target, "\\") {
		return true
	}
	joined := path.Clean(path.Join(path.Dir(name), target))
	return joined == ".." || strings.HasPrefix(joined, "../")
}
func appendSuspicious(report *Report, name, reason string) {
	if len(report.SuspiciousEntries) < 100 {
		report.SuspiciousEntries = append(report.SuspiciousEntries, SuspiciousEntry{Path: safeArchivePath(name), Reason: reason})
	}
}
func safeArchivePath(name string) string {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	for clean == ".." || strings.HasPrefix(clean, "../") {
		clean = strings.TrimPrefix(clean, "../")
	}
	if clean == "." {
		return ""
	}
	return clean
}
