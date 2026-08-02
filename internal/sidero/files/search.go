// Package files implements bounded server-root-scoped filesystem services.
package files

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	iofs "io/fs"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var (
	ErrInvalidPath  = errors.New("sidero files: invalid relative path")
	ErrLimitReached = errors.New("sidero files: result limit reached")
)

const maximumClientPathBytes = 4096
const maximumSearchQueryBytes = 4096
const maximumExtensionFilters = 100

func NormalizeRelativePath(value string) (string, error) {
	if len(value) > maximumClientPathBytes || strings.IndexByte(value, 0) >= 0 || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || len(value) >= 2 && value[1] == ':' && (value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z') {
		return "", ErrInvalidPath
	}
	clean := path.Clean(value)
	if clean == "/" || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", ErrInvalidPath
	}
	if clean == "" {
		clean = "."
	}
	return clean, nil
}

func NormalizeClientPath(value string) (string, error) {
	clean, err := NormalizeRelativePath(value)
	if err != nil || clean == ".sidero" || strings.HasPrefix(clean, ".sidero/") {
		return "", ErrInvalidPath
	}
	return clean, nil
}

type SearchRequest struct {
	Root               string   `json:"root"`
	Query              string   `json:"query"`
	Recursive          bool     `json:"recursive"`
	CaseSensitive      bool     `json:"case_sensitive"`
	IncludeFiles       bool     `json:"include_files"`
	IncludeDirectories bool     `json:"include_directories"`
	AllowedExtensions  []string `json:"allowed_extensions"`
	BlockedExtensions  []string `json:"blocked_extensions"`
	MaximumDepth       int      `json:"maximum_depth"`
	MaximumResults     int      `json:"maximum_results"`
	MaximumEntries     int      `json:"maximum_entries"`
}

type Entry struct {
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	Size         int64     `json:"size"`
	ModifiedTime time.Time `json:"modified_at"`
}

type SearchResult struct {
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

func Search(ctx context.Context, fs *wfs.Filesystem, req SearchRequest) (SearchResult, error) {
	root, err := NormalizeClientPath(req.Root)
	if err != nil {
		return SearchResult{}, err
	}
	if req.MaximumDepth < 0 || req.MaximumResults < 1 || req.MaximumEntries < 1 || len(req.Query) > maximumSearchQueryBytes || strings.TrimSpace(req.Query) == "" || !validExtensionFilters(req.AllowedExtensions, req.BlockedExtensions) {
		return SearchResult{}, ErrInvalidPath
	}
	if !req.IncludeFiles && !req.IncludeDirectories {
		req.IncludeFiles, req.IncludeDirectories = true, true
	}
	query := req.Query
	if !req.CaseSensitive {
		query = strings.ToLower(query)
	}
	allowed, blocked := extensionSet(req.AllowedExtensions), extensionSet(req.BlockedExtensions)
	out := SearchResult{Entries: make([]Entry, 0, min(req.MaximumResults, 128))}
	visited := 0
	var walk func(string, int) error
	walk = func(directory string, directoryDepth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := fs.ReadDir(directory)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, item := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			visited++
			if visited > req.MaximumEntries {
				out.Truncated = true
				return nil
			}
			rel := path.Join(directory, item.Name())
			if directory == "." {
				rel = item.Name()
			}
			info, err := fs.UnixFS().Lstat(rel)
			if err != nil {
				return err
			}
			if info.Mode()&iofs.ModeSymlink != 0 {
				continue
			}
			name := item.Name()
			candidate := name
			if !req.CaseSensitive {
				candidate = strings.ToLower(candidate)
			}
			matches := strings.Contains(candidate, query) && extensionAllowed(name, info.IsDir(), allowed, blocked)
			include := info.IsDir() && req.IncludeDirectories || !info.IsDir() && info.Mode().IsRegular() && req.IncludeFiles
			if matches && include {
				if len(out.Entries) >= req.MaximumResults {
					out.Truncated = true
					return nil
				}
				kind := "file"
				if info.IsDir() {
					kind = "directory"
				}
				out.Entries = append(out.Entries, Entry{Path: rel, Name: name, Type: kind, Size: info.Size(), ModifiedTime: info.ModTime().UTC()})
			}
			if info.IsDir() && req.Recursive && directoryDepth < req.MaximumDepth {
				if err := walk(rel, directoryDepth+1); err != nil {
					return err
				}
				if out.Truncated {
					return nil
				}
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return SearchResult{}, err
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out, nil
}

type ContentSearchRequest struct {
	Root                  string   `json:"root"`
	Query                 string   `json:"query"`
	CaseSensitive         bool     `json:"case_sensitive"`
	AllowedExtensions     []string `json:"allowed_extensions"`
	BlockedExtensions     []string `json:"blocked_extensions"`
	MaximumFileSize       int64    `json:"maximum_file_size"`
	MaximumMatchesPerFile int      `json:"maximum_matches_per_file"`
	MaximumTotalMatches   int      `json:"maximum_total_matches"`
	MaximumExcerptBytes   int      `json:"maximum_excerpt_bytes"`
	MaximumDepth          int      `json:"maximum_depth"`
	MaximumFilesScanned   int      `json:"maximum_files_scanned"`
}

type ContentMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Excerpt string `json:"excerpt"`
}
type ContentSearchResult struct {
	Matches      []ContentMatch `json:"matches"`
	FilesScanned int            `json:"files_scanned"`
	Truncated    bool           `json:"truncated"`
}

func ContentSearch(ctx context.Context, fs *wfs.Filesystem, req ContentSearchRequest) (ContentSearchResult, error) {
	root, err := NormalizeClientPath(req.Root)
	if err != nil {
		return ContentSearchResult{}, err
	}
	if strings.TrimSpace(req.Query) == "" || len(req.Query) > maximumSearchQueryBytes || req.MaximumFileSize < 1 || req.MaximumMatchesPerFile < 1 || req.MaximumTotalMatches < 1 || req.MaximumExcerptBytes < 1 || !validExtensionFilters(req.AllowedExtensions, req.BlockedExtensions) {
		return ContentSearchResult{}, ErrInvalidPath
	}
	if req.MaximumDepth <= 0 {
		req.MaximumDepth = 64
	}
	if req.MaximumFilesScanned <= 0 {
		req.MaximumFilesScanned = 10000
	}
	allowed, blocked := extensionSet(req.AllowedExtensions), extensionSet(req.BlockedExtensions)
	out := ContentSearchResult{Matches: make([]ContentMatch, 0, min(req.MaximumTotalMatches, 128))}
	visitedFiles := 0
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		entries, err := fs.ReadDir(directory)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, item := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			rel := path.Join(directory, item.Name())
			if directory == "." {
				rel = item.Name()
			}
			info, err := fs.UnixFS().Lstat(rel)
			if err != nil {
				return err
			}
			if info.Mode()&iofs.ModeSymlink != 0 {
				continue
			}
			if info.IsDir() {
				if depth >= req.MaximumDepth {
					continue
				}
				if err := walk(rel, depth+1); err != nil {
					return err
				}
				if out.Truncated {
					return nil
				}
				continue
			}
			if !info.Mode().IsRegular() || info.Size() > req.MaximumFileSize || !extensionAllowed(item.Name(), false, allowed, blocked) {
				continue
			}
			visitedFiles++
			if visitedFiles > req.MaximumFilesScanned {
				out.Truncated = true
				return nil
			}
			matches, binary, err := searchFile(ctx, fs, rel, req)
			if err != nil {
				return err
			}
			if binary {
				continue
			}
			out.FilesScanned++
			for _, match := range matches {
				if len(out.Matches) >= req.MaximumTotalMatches {
					out.Truncated = true
					return nil
				}
				out.Matches = append(out.Matches, match)
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return ContentSearchResult{}, err
	}
	return out, nil
}

func searchFile(ctx context.Context, fs *wfs.Filesystem, rel string, req ContentSearchRequest) ([]ContentMatch, bool, error) {
	f, _, err := fs.File(rel)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	prefix := make([]byte, min(int(req.MaximumFileSize), 8192))
	n, readErr := io.ReadFull(f, prefix)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return nil, false, readErr
	}
	if bytes.IndexByte(prefix[:n], 0) >= 0 {
		return nil, true, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	needle := req.Query
	if !req.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	scanner := bufio.NewScanner(io.LimitReader(f, req.MaximumFileSize+1))
	scanner.Buffer(make([]byte, 64*1024), int(req.MaximumFileSize)+1)
	matches := make([]ContentMatch, 0)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		value := scanner.Text()
		candidate := value
		if !req.CaseSensitive {
			candidate = strings.ToLower(candidate)
		}
		if !strings.Contains(candidate, needle) {
			continue
		}
		excerpt := truncateUTF8(value, req.MaximumExcerptBytes)
		matches = append(matches, ContentMatch{Path: rel, Line: line, Excerpt: html.EscapeString(excerpt)})
		if len(matches) >= req.MaximumMatchesPerFile {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return matches, false, nil
}

func extensionSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, ".") {
			value = "." + value
		}
		out[value] = struct{}{}
	}
	return out
}
func validExtensionFilters(groups ...[]string) bool {
	total := 0
	for _, values := range groups {
		total += len(values)
		for _, value := range values {
			if len(value) > 32 {
				return false
			}
		}
	}
	return total <= maximumExtensionFilters
}
func extensionAllowed(name string, directory bool, allowed, blocked map[string]struct{}) bool {
	if directory {
		return true
	}
	ext := strings.ToLower(path.Ext(name))
	if _, found := blocked[ext]; found {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	_, found := allowed[ext]
	return found
}
func truncateUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	b := []byte(value[:maximum])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}
