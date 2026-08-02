package archives

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractStagesAndCommitsSafeArchive(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	writeZip(t, filepath.Join(root, "world.zip"), map[string][]byte{
		"level.dat":        []byte("level"),
		"region/r.0.0.mca": []byte("region"),
	})
	result, err := Extract(context.Background(), fs, ExtractRequest{Archive: "world.zip", Destination: "world", ConflictPolicy: "fail"}, Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 512, MaximumCompressionRatio: 200}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, result.FileCount)
	content, err := os.ReadFile(filepath.Join(root, "world", "level.dat"))
	require.NoError(t, err)
	require.Equal(t, "level", string(content))
	require.NoDirExists(t, filepath.Join(root, ".sidero", "archives", result.OperationID))
}

func TestExtractRejectsTraversalDuringExtractionAndCleansStage(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	var data bytes.Buffer
	tw := tar.NewWriter(&data)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 1}))
	_, err := tw.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(root, "unsafe.tar"), data.Bytes(), 0o644))
	_, err = Extract(context.Background(), fs, ExtractRequest{Archive: "unsafe.tar", Destination: "output", ConflictPolicy: "fail"}, Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 512, MaximumCompressionRatio: 200}, nil)
	require.ErrorIs(t, err, ErrTraversal)
	require.NoFileExists(t, filepath.Join(root, "escape.txt"))
	require.NoDirExists(t, filepath.Join(root, "output"))
}

func TestExtractRejectsSymlinkAndDuplicateEntries(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	file, err := os.Create(filepath.Join(root, "links.zip"))
	require.NoError(t, err)
	zw := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(header)
	require.NoError(t, err)
	_, err = w.Write([]byte("../../outside"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, file.Close())
	_, err = Extract(context.Background(), fs, ExtractRequest{Archive: "links.zip", Destination: "output", ConflictPolicy: "fail"}, Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 512, MaximumCompressionRatio: 200}, nil)
	require.ErrorIs(t, err, ErrUnsafeEntry)
}

func TestExtractReplacePreservesRollbackBackupUntilCommit(t *testing.T) {
	fs, root := archiveTestFilesystem(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "world"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "world", "old.txt"), []byte("old"), 0o644))
	writeZip(t, filepath.Join(root, "new.zip"), map[string][]byte{"level.dat": []byte("new")})
	_, err := Extract(context.Background(), fs, ExtractRequest{Archive: "new.zip", Destination: "world", ConflictPolicy: "replace"}, Limits{MaximumEntries: 10, MaximumUncompressedBytes: 1024, MaximumSingleEntryBytes: 512, MaximumCompressionRatio: 200}, nil)
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(root, "world", "old.txt"))
	require.FileExists(t, filepath.Join(root, "world", "level.dat"))
}
