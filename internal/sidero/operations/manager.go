// Package operations provides the bounded, in-memory operation runtime shared by
// all Sidero long-running features.
package operations

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	ErrLimitReached      = errors.New("sidero operations: queue limit reached")
	ErrClosed            = errors.New("sidero operations: manager closed")
	ErrInvalidSubmission = errors.New("sidero operations: invalid submission")
)

type State string

const (
	StateQueued     State = "queued"
	StateValidating State = "validating"
	StateRunning    State = "running"
	StateFinalizing State = "finalizing"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
)

func (s State) Terminal() bool { return s == StateCompleted || s == StateFailed || s == StateCancelled }

type SafeError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type Operation struct {
	ID             string     `json:"id"`
	ServerID       string     `json:"-"`
	Type           string     `json:"type"`
	State          State      `json:"state"`
	Progress       float64    `json:"progress"`
	Message        string     `json:"message,omitempty"`
	Result         any        `json:"result,omitempty"`
	Error          *SafeError `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	IdempotencyKey string     `json:"-"`
}

type Event struct {
	ServerID string         `json:"-"`
	Name     string         `json:"-"`
	Payload  map[string]any `json:"payload"`
}

type Config struct {
	MaximumConcurrentGlobal    int
	MaximumConcurrentPerServer int
	Retention                  time.Duration
	QueueSize                  int
}

type UpdateFunc func(progress float64, message string)
type Runner func(context.Context, UpdateFunc) (any, *SafeError)
type PublishFunc func(Event)

type job struct {
	id  string
	run Runner
}
type record struct {
	operation Operation
	cancel    context.CancelFunc
}

type Manager struct {
	cfg         Config
	mu          sync.RWMutex
	records     map[string]*record
	idempotency map[string]string
	perServer   map[string]chan struct{}
	jobs        chan job
	closed      chan struct{}
	closeOnce   sync.Once
	wg          sync.WaitGroup
	publish     PublishFunc
	running     atomic.Int64
}

func NewManager(cfg Config, publish PublishFunc) *Manager {
	if cfg.MaximumConcurrentGlobal < 1 {
		cfg.MaximumConcurrentGlobal = 1
	}
	if cfg.MaximumConcurrentPerServer < 1 {
		cfg.MaximumConcurrentPerServer = 1
	}
	if cfg.QueueSize < 1 {
		cfg.QueueSize = cfg.MaximumConcurrentGlobal * 4
	}
	if cfg.Retention <= 0 {
		cfg.Retention = time.Hour
	}
	m := &Manager{cfg: cfg, records: map[string]*record{}, idempotency: map[string]string{}, perServer: map[string]chan struct{}{}, jobs: make(chan job, cfg.QueueSize), closed: make(chan struct{}), publish: publish}
	for i := 0; i < cfg.MaximumConcurrentGlobal; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m
}

func (m *Manager) Submit(serverID, operationType, idempotencyKey string, runner Runner) (Operation, error) {
	if serverID == "" || operationType == "" || len(idempotencyKey) > 256 || runner == nil {
		return Operation{}, ErrInvalidSubmission
	}
	m.mu.Lock()
	if idempotencyKey != "" {
		key := serverID + "\x00" + operationType + "\x00" + idempotencyKey
		if id, ok := m.idempotency[key]; ok {
			op := m.records[id].operation
			m.mu.Unlock()
			return op, nil
		}
	}
	now := time.Now().UTC()
	op := Operation{ID: uuid.NewString(), ServerID: serverID, Type: operationType, State: StateQueued, CreatedAt: now, IdempotencyKey: idempotencyKey}
	m.records[op.ID] = &record{operation: op}
	if idempotencyKey != "" {
		m.idempotency[serverID+"\x00"+operationType+"\x00"+idempotencyKey] = op.ID
	}
	m.mu.Unlock()
	select {
	case <-m.closed:
		m.remove(op.ID)
		return Operation{}, ErrClosed
	case m.jobs <- job{id: op.ID, run: runner}:
		m.emit(op, "sidero operation created")
		return op, nil
	default:
		m.remove(op.ID)
		return Operation{}, ErrLimitReached
	}
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.closed:
			return
		case j := <-m.jobs:
			m.execute(j)
		}
	}
}

func (m *Manager) execute(j job) {
	m.mu.Lock()
	r, ok := m.records[j.id]
	if !ok {
		m.mu.Unlock()
		return
	}
	serverID := r.operation.ServerID
	sem := m.perServer[serverID]
	if sem == nil {
		sem = make(chan struct{}, m.cfg.MaximumConcurrentPerServer)
		m.perServer[serverID] = sem
	}
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	select {
	case sem <- struct{}{}:
	case <-m.closed:
		cancel()
		return
	case <-ctx.Done():
		cancel()
		return
	}
	defer func() { <-sem }()
	m.mu.Lock()
	r, ok = m.records[j.id]
	if !ok {
		m.mu.Unlock()
		cancel()
		return
	}
	if r.operation.State == StateCancelled {
		m.mu.Unlock()
		cancel()
		return
	}
	now := time.Now().UTC()
	r.cancel = cancel
	r.operation.State = StateValidating
	r.operation.StartedAt = &now
	op := r.operation
	m.running.Add(1)
	m.mu.Unlock()
	m.emit(op, "sidero operation progress")
	m.mu.Lock()
	r, ok = m.records[j.id]
	if !ok || r.operation.State == StateCancelled {
		m.mu.Unlock()
		cancel()
		m.running.Add(-1)
		return
	}
	r.operation.State = StateRunning
	op = r.operation
	m.mu.Unlock()
	m.emit(op, "sidero operation progress")

	update := func(progress float64, message string) {
		if progress < 0 {
			progress = 0
		}
		if progress > 1 {
			progress = 1
		}
		m.mu.Lock()
		r, ok := m.records[j.id]
		if !ok || r.operation.State.Terminal() {
			m.mu.Unlock()
			return
		}
		r.operation.Progress, r.operation.Message = progress, message
		op := r.operation
		m.mu.Unlock()
		m.emit(op, "sidero operation progress")
	}
	result, safeErr := j.run(ctx, update)
	if safeErr == nil {
		m.mu.Lock()
		if r, ok := m.records[j.id]; ok && r.operation.State != StateCancelled {
			r.operation.State = StateFinalizing
			op = r.operation
			m.mu.Unlock()
			m.emit(op, "sidero operation progress")
		} else {
			m.mu.Unlock()
		}
	}
	cancel()
	m.running.Add(-1)
	m.mu.Lock()
	r, ok = m.records[j.id]
	if !ok {
		m.mu.Unlock()
		return
	}
	alreadyCancelled := r.operation.State == StateCancelled
	finished := time.Now().UTC()
	r.operation.CompletedAt = &finished
	if r.operation.State == StateCancelled || ctx.Err() == context.Canceled && safeErr != nil && safeErr.Code == "operation_cancelled" {
		r.operation.State = StateCancelled
		r.operation.Error = &SafeError{Code: "operation_cancelled", Message: "The operation was cancelled."}
	} else if safeErr != nil {
		r.operation.State = StateFailed
		r.operation.Error = safeErr
	} else {
		r.operation.State = StateCompleted
		r.operation.Progress = 1
		r.operation.Result = result
	}
	op = r.operation
	m.mu.Unlock()
	if alreadyCancelled {
		return
	}
	name := "sidero operation completed"
	if op.State == StateFailed {
		name = "sidero operation failed"
	}
	if op.State == StateCancelled {
		name = "sidero operation cancelled"
	}
	m.emit(op, name)
}

func (m *Manager) Get(serverID, id string) (Operation, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.records[id]
	if !ok || r.operation.ServerID != serverID {
		return Operation{}, false
	}
	return r.operation, true
}

func (m *Manager) List(serverID string) []Operation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Operation, 0)
	for _, r := range m.records {
		if r.operation.ServerID == serverID {
			out = append(out, r.operation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (m *Manager) Cancel(serverID, id string) bool {
	m.mu.Lock()
	r, ok := m.records[id]
	if !ok || r.operation.ServerID != serverID || r.operation.State.Terminal() {
		m.mu.Unlock()
		return false
	}
	r.operation.State = StateCancelled
	r.operation.Error = &SafeError{Code: "operation_cancelled", Message: "The operation was cancelled."}
	now := time.Now().UTC()
	r.operation.CompletedAt = &now
	if r.cancel != nil {
		r.cancel()
	}
	op := r.operation
	m.mu.Unlock()
	m.emit(op, "sidero operation cancelled")
	return true
}

func (m *Manager) Cleanup(now time.Time, maximum int) int {
	if maximum < 1 {
		return 0
	}
	removed := 0
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.records {
		if removed >= maximum {
			break
		}
		if !r.operation.State.Terminal() || r.operation.CompletedAt == nil || now.Sub(*r.operation.CompletedAt) < m.cfg.Retention {
			continue
		}
		delete(m.records, id)
		if r.operation.IdempotencyKey != "" {
			delete(m.idempotency, r.operation.ServerID+"\x00"+r.operation.Type+"\x00"+r.operation.IdempotencyKey)
		}
		removed++
	}
	activeServers := make(map[string]struct{}, len(m.records))
	for _, r := range m.records {
		activeServers[r.operation.ServerID] = struct{}{}
	}
	for serverID, semaphore := range m.perServer {
		if len(semaphore) != 0 {
			continue
		}
		if _, active := activeServers[serverID]; !active {
			delete(m.perServer, serverID)
		}
	}
	return removed
}

func (m *Manager) Running() int { return int(m.running.Load()) }

func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		close(m.closed)
		m.mu.Lock()
		for _, r := range m.records {
			if r.cancel != nil {
				r.cancel()
			}
		}
		m.mu.Unlock()
		m.wg.Wait()
	})
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.records[id]
	if r == nil {
		return
	}
	delete(m.records, id)
	if r.operation.IdempotencyKey != "" {
		delete(m.idempotency, r.operation.ServerID+"\x00"+r.operation.Type+"\x00"+r.operation.IdempotencyKey)
	}
}

func (m *Manager) emit(op Operation, name string) {
	if m.publish == nil {
		return
	}
	p := map[string]any{"operation_id": op.ID, "type": op.Type, "state": op.State, "progress": op.Progress}
	if op.Message != "" {
		p["message"] = op.Message
	}
	if op.Result != nil && op.State == StateCompleted {
		p["result"] = map[string]any{"available": true}
	}
	if op.Error != nil {
		p["error_code"] = op.Error.Code
	}
	m.publish(Event{ServerID: op.ServerID, Name: name, Payload: p})
}
