package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumStateBytes = 1 << 20

type PersistedRule struct {
	ServerID string `json:"server_id"`
	Rule
}

type persistedState struct {
	Version int             `json:"version"`
	Rules   []PersistedRule `json:"rules"`
}

type FileStore struct{ path string }

func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

func (s *FileStore) Load() ([]PersistedRule, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []PersistedRule{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximumStateBytes {
		return nil, errors.New("sidero firewall: invalid state file")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maximumStateBytes+1))
	decoder.DisallowUnknownFields()
	var state persistedState
	if err := decoder.Decode(&state); err != nil {
		return nil, errors.New("sidero firewall: invalid state data")
	}
	if state.Version != 1 || len(state.Rules) > 10000 {
		return nil, errors.New("sidero firewall: unsupported state data")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("sidero firewall: trailing state data")
	}
	return state.Rules, nil
}

func (s *FileStore) Save(ctx context.Context, rules []AppliedRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Lstat(s.path); err == nil && !info.Mode().IsRegular() {
		return errors.New("sidero firewall: unsafe state file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("sidero firewall: unsafe state directory")
	}
	state := persistedState{Version: 1, Rules: make([]PersistedRule, 0, len(rules))}
	for _, rule := range rules {
		state.Rules = append(state.Rules, PersistedRule{ServerID: rule.ServerID, Rule: rule.Rule})
	}
	temporary, err := os.CreateTemp(directory, ".sidero-firewall-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(state); err != nil {
		_ = temporary.Close()
		return err
	}
	if position, err := temporary.Seek(0, io.SeekCurrent); err != nil || position > maximumStateBytes {
		_ = temporary.Close()
		return fmt.Errorf("sidero firewall: state exceeds limit")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, s.path)
}
