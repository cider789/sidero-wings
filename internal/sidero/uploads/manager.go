// Package uploads implements bounded server-scoped resumable upload sessions.
package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/ufs"
	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var (
	ErrNotFound     = errors.New("sidero uploads: session not found")
	ErrExpired      = errors.New("sidero uploads: session expired")
	ErrChunkInvalid = errors.New("sidero uploads: invalid chunk")
	ErrIncomplete   = errors.New("sidero uploads: incomplete")
	ErrChecksum     = errors.New("sidero uploads: checksum mismatch")
	ErrLimit        = errors.New("sidero uploads: limit reached")
	ErrConflict     = errors.New("sidero uploads: destination conflict")
)

type Config struct {
	ChunkSize          int64
	SessionExpiry      time.Duration
	MaximumUploadBytes int64
	MaximumGlobal      int
	MaximumPerServer   int
	Publish            func(serverID, event string, payload map[string]any)
}
type CreateRequest struct {
	Destination       string `json:"destination"`
	Filename          string `json:"filename"`
	ExpectedTotalSize int64  `json:"expected_total_size"`
	ChecksumAlgorithm string `json:"checksum_algorithm,omitempty"`
	ExpectedChecksum  string `json:"expected_checksum,omitempty"`
	ConflictPolicy    string `json:"conflict_policy"`
}
type Session struct {
	ID                 string    `json:"id"`
	ServerID           string    `json:"-"`
	Destination        string    `json:"destination"`
	Filename           string    `json:"filename"`
	ExpectedTotalSize  int64     `json:"expected_total_size"`
	ChunkSize          int64     `json:"chunk_size"`
	ExpectedChunkCount int       `json:"expected_chunk_count"`
	ReceivedChunks     []int     `json:"received_chunks"`
	ReceivedBytes      int64     `json:"received_bytes"`
	CreatedAt          time.Time `json:"created_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	State              string    `json:"state"`
}
type CompleteResult struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Checksum string `json:"checksum,omitempty"`
}
type record struct {
	session    Session
	filesystem *wfs.Filesystem
	request    CreateRequest
	chunks     map[int]string
	chunkBytes map[int]int64
}
type Manager struct {
	cfg      Config
	mu       sync.RWMutex
	sessions map[string]*record
}

func NewManager(cfg Config) *Manager {
	if cfg.MaximumGlobal < 1 {
		cfg.MaximumGlobal = 16
	}
	if cfg.MaximumPerServer < 1 {
		cfg.MaximumPerServer = 3
	}
	return &Manager{cfg: cfg, sessions: map[string]*record{}}
}

func (m *Manager) Create(serverID string, filesystem *wfs.Filesystem, request CreateRequest) (Session, error) {
	destination, err := files.NormalizeClientPath(request.Destination)
	if err != nil || path.Base(request.Filename) != request.Filename || request.Filename == "" || request.Filename == "." || request.Filename == ".." || strings.Contains(request.Filename, "\\") || request.ExpectedTotalSize < 0 || request.ExpectedTotalSize > m.cfg.MaximumUploadBytes {
		return Session{}, ErrChunkInvalid
	}
	if request.ConflictPolicy == "" {
		request.ConflictPolicy = "fail"
	}
	if request.ConflictPolicy != "fail" && request.ConflictPolicy != "skip" && request.ConflictPolicy != "replace" {
		return Session{}, ErrChunkInvalid
	}
	if _, _, err := newHash(request.ChecksumAlgorithm, request.ExpectedChecksum); err != nil {
		return Session{}, ErrChunkInvalid
	}
	if err := filesystem.HasSpaceFor(request.ExpectedTotalSize); err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= m.cfg.MaximumGlobal {
		return Session{}, ErrLimit
	}
	count := 0
	for _, r := range m.sessions {
		if r.session.ServerID == serverID {
			count++
		}
	}
	if count >= m.cfg.MaximumPerServer {
		return Session{}, ErrLimit
	}
	now := time.Now().UTC()
	countChunks := 0
	if request.ExpectedTotalSize > 0 {
		countChunks = int((request.ExpectedTotalSize + m.cfg.ChunkSize - 1) / m.cfg.ChunkSize)
	}
	s := Session{ID: uuid.NewString(), ServerID: serverID, Destination: destination, Filename: request.Filename, ExpectedTotalSize: request.ExpectedTotalSize, ChunkSize: m.cfg.ChunkSize, ExpectedChunkCount: countChunks, ReceivedChunks: []int{}, CreatedAt: now, ExpiresAt: now.Add(m.cfg.SessionExpiry), State: "pending"}
	m.sessions[s.ID] = &record{session: s, filesystem: filesystem, request: request, chunks: map[int]string{}, chunkBytes: map[int]int64{}}
	m.emit(s, "sidero upload created")
	return s, nil
}

func (m *Manager) Get(serverID, id string) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.sessions[id]
	if r == nil || r.session.ServerID != serverID {
		return Session{}, ErrNotFound
	}
	if time.Now().After(r.session.ExpiresAt) {
		return Session{}, ErrExpired
	}
	return copySession(r), nil
}

func (m *Manager) PutChunk(ctx context.Context, serverID, id string, index int, reader io.Reader, contentLength int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.sessions[id]
	if r == nil || r.session.ServerID != serverID {
		return ErrNotFound
	}
	if time.Now().After(r.session.ExpiresAt) {
		return ErrExpired
	}
	if r.session.State != "pending" {
		return ErrConflict
	}
	if index < 0 || index >= r.session.ExpectedChunkCount {
		return ErrChunkInvalid
	}
	expectedSize := r.session.ChunkSize
	if index == r.session.ExpectedChunkCount-1 {
		expectedSize = r.session.ExpectedTotalSize - int64(index)*r.session.ChunkSize
	}
	if contentLength != expectedSize {
		return ErrChunkInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(reader, expectedSize+1))
	if err != nil || int64(len(data)) != expectedSize {
		return ErrChunkInvalid
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if previous, ok := r.chunks[index]; ok {
		if subtle.ConstantTimeCompare([]byte(previous), []byte(digest)) == 1 {
			return nil
		}
		return ErrChunkInvalid
	}
	chunkPath := path.Join(".sidero", "uploads", id, "chunks", chunkName(index))
	if err := r.filesystem.Write(chunkPath, bytes.NewReader(data), int64(len(data)), 0o600); err != nil {
		return err
	}
	r.chunks[index], r.chunkBytes[index] = digest, int64(len(data))
	r.session.ReceivedBytes += int64(len(data))
	r.session.ReceivedChunks = append(r.session.ReceivedChunks, index)
	sort.Ints(r.session.ReceivedChunks)
	m.emit(r.session, "sidero upload progress")
	return nil
}

func (m *Manager) Complete(ctx context.Context, serverID, id string) (CompleteResult, error) {
	m.mu.Lock()
	r := m.sessions[id]
	if r == nil || r.session.ServerID != serverID {
		m.mu.Unlock()
		return CompleteResult{}, ErrNotFound
	}
	if time.Now().After(r.session.ExpiresAt) {
		m.mu.Unlock()
		return CompleteResult{}, ErrExpired
	}
	if len(r.chunks) != r.session.ExpectedChunkCount || r.session.ReceivedBytes != r.session.ExpectedTotalSize {
		m.mu.Unlock()
		return CompleteResult{}, ErrIncomplete
	}
	if r.session.State != "pending" {
		m.mu.Unlock()
		return CompleteResult{}, ErrConflict
	}
	r.session.State = "finalizing"
	m.mu.Unlock()
	assembled := path.Join(".sidero", "uploads", id, "assembled.part")
	defer func() {
		_ = r.filesystem.Delete(assembled)
		m.mu.Lock()
		if current := m.sessions[id]; current != nil && current.session.State == "finalizing" {
			current.session.State = "pending"
		}
		m.mu.Unlock()
	}()
	out, err := r.filesystem.Touch(assembled, ufs.O_WRONLY|ufs.O_CREATE|ufs.O_EXCL)
	if err != nil {
		return CompleteResult{}, err
	}
	h, expected, _ := newHash(r.request.ChecksumAlgorithm, r.request.ExpectedChecksum)
	writer := io.Writer(out)
	if h != nil {
		writer = io.MultiWriter(out, h)
	}
	var written int64
	for index := 0; index < r.session.ExpectedChunkCount; index++ {
		if err := ctx.Err(); err != nil {
			out.Close()
			return CompleteResult{}, err
		}
		chunk, _, err := r.filesystem.File(path.Join(".sidero", "uploads", id, "chunks", chunkName(index)))
		if err != nil {
			out.Close()
			return CompleteResult{}, err
		}
		n, copyErr := io.Copy(writer, io.LimitReader(chunk, r.chunkBytes[index]))
		closeErr := chunk.Close()
		written += n
		if copyErr != nil {
			out.Close()
			return CompleteResult{}, copyErr
		}
		if closeErr != nil {
			out.Close()
			return CompleteResult{}, closeErr
		}
	}
	if err := out.Close(); err != nil {
		return CompleteResult{}, err
	}
	if written != r.session.ExpectedTotalSize {
		return CompleteResult{}, ErrIncomplete
	}
	actual := ""
	if h != nil {
		actual = hex.EncodeToString(h.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return CompleteResult{}, ErrChecksum
		}
	}
	if err := r.filesystem.Chown(assembled); err != nil {
		return CompleteResult{}, err
	}
	target := path.Join(r.session.Destination, r.session.Filename)
	_, statErr := r.filesystem.UnixFS().Lstat(target)
	if statErr == nil {
		switch r.request.ConflictPolicy {
		case "fail":
			return CompleteResult{}, ErrConflict
		case "skip":
			_ = r.filesystem.Delete(path.Join(".sidero", "uploads", id))
			m.remove(id)
			r.session.State = "completed"
			m.emit(r.session, "sidero upload completed")
			return CompleteResult{Path: target}, nil
		case "replace":
			err = r.filesystem.AtomicReplace(assembled, target)
		}
	} else if errors.Is(statErr, ufs.ErrNotExist) {
		err = r.filesystem.Rename(assembled, target)
	} else {
		err = statErr
	}
	if err != nil {
		return CompleteResult{}, err
	}
	_ = r.filesystem.Delete(path.Join(".sidero", "uploads", id))
	m.remove(id)
	r.session.State = "completed"
	m.emit(r.session, "sidero upload completed")
	return CompleteResult{Path: target, Bytes: written, Checksum: actual}, nil
}

func (m *Manager) Cancel(serverID, id string) error {
	m.mu.Lock()
	r := m.sessions[id]
	if r == nil || r.session.ServerID != serverID {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	err := r.filesystem.Delete(path.Join(".sidero", "uploads", id))
	r.session.State = "cancelled"
	m.emit(r.session, "sidero upload cancelled")
	return err
}
func (m *Manager) Cleanup(now time.Time, maximum int) int {
	m.mu.Lock()
	candidates := make([]*record, 0)
	for id, r := range m.sessions {
		if len(candidates) >= maximum {
			break
		}
		if now.After(r.session.ExpiresAt) {
			candidates = append(candidates, r)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	for _, r := range candidates {
		_ = r.filesystem.Delete(path.Join(".sidero", "uploads", r.session.ID))
	}
	return len(candidates)
}
func (m *Manager) remove(id string) { m.mu.Lock(); delete(m.sessions, id); m.mu.Unlock() }
func (m *Manager) emit(session Session, event string) {
	if m.cfg.Publish == nil {
		return
	}
	m.cfg.Publish(session.ServerID, event, map[string]any{"upload_id": session.ID, "state": session.State, "received_bytes": session.ReceivedBytes, "expected_total_size": session.ExpectedTotalSize})
}
func copySession(r *record) Session {
	s := r.session
	s.ReceivedChunks = append([]int(nil), s.ReceivedChunks...)
	return s
}
func chunkName(index int) string {
	const digits = "0123456789"
	if index == 0 {
		return "00000000.part"
	}
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = digits[index%10]
		index /= 10
	}
	return string(b) + ".part"
}
func newHash(algorithm, expected string) (hash.Hash, string, error) {
	algorithm, expected = strings.ToLower(strings.TrimSpace(algorithm)), strings.ToLower(strings.TrimSpace(expected))
	if algorithm == "" && expected == "" {
		return nil, "", nil
	}
	switch algorithm {
	case "sha256":
		if len(expected) != sha256.Size*2 {
			return nil, "", ErrChecksum
		}
		decoded, err := hex.DecodeString(expected)
		if err != nil {
			return nil, "", err
		}
		if len(decoded) != sha256.Size {
			return nil, "", ErrChecksum
		}
		return sha256.New(), expected, nil
	case "sha512":
		if len(expected) != sha512.Size*2 {
			return nil, "", ErrChecksum
		}
		decoded, err := hex.DecodeString(expected)
		if err != nil {
			return nil, "", err
		}
		if len(decoded) != sha512.Size {
			return nil, "", ErrChecksum
		}
		return sha512.New(), expected, nil
	default:
		return nil, "", ErrChecksum
	}
}
