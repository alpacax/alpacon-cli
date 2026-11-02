package localsignal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
)

type entry struct {
	Last   time.Time `json:"last"`
	Folded int       `json:"folded"`
}

func allow(statePath, workspace, session string, kind Kind, clock func() time.Time) (folded int, emit bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	type result struct {
		folded int
		emit   bool
		err    error
	}
	done := make(chan result, 1)
	go func() {
		folded, emit, err := allowWithin(ctx, statePath, workspace, session, kind, clock)
		done <- result{folded, emit, err}
	}()
	select {
	case r := <-done:
		return r.folded, r.emit, r.err
	case <-ctx.Done():
		return 0, true, filelock.ErrLocked
	}
}

func allowWithin(ctx context.Context, statePath, workspace, session string, kind Kind, clock func() time.Time) (int, bool, error) {
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return 0, true, err
	}
	// Hold a sibling lock: renaming the state file replaces its inode.
	lock, err := openSignalFile(statePath+".lock", true)
	if err != nil {
		return 0, true, err
	}
	defer func() { _ = lock.Close() }()
	for {
		err = lockSignalFile(lock)
		if !errors.Is(err, filelock.ErrLocked) {
			break
		}
		select {
		case <-ctx.Done():
			return 0, true, filelock.ErrLocked
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err != nil {
		return 0, true, err
	}
	defer func() { _ = unlockSignalFile(lock) }()
	if err := ctx.Err(); err != nil {
		return 0, true, err
	}
	now := clock()
	state := map[string]entry{}
	f, readErr := openSignalFile(statePath, false)
	if readErr == nil {
		data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
		_ = f.Close()
		if err != nil {
			return 0, true, err
		}
		if len(data) > 64*1024 || json.Unmarshal(data, &state) != nil || state == nil {
			state = map[string]entry{}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return 0, true, readErr
	}
	for key, item := range state {
		if item.Last.After(now) || now.Sub(item.Last) > 24*time.Hour {
			delete(state, key)
		}
	}
	session = capText(session, 128)
	key := string(kind)
	if workspace != "" {
		key = workspace + "|" + key
	}
	if session != "" {
		key += ":" + session
	}
	last, found := state[key]
	if found && !last.Last.After(now) && now.Sub(last.Last) < 60*time.Second {
		last.Folded++
		state[key] = last
		err := saveState(ctx, statePath, state)
		return 0, err != nil, err
	}
	state[key] = entry{Last: now}
	return last.Folded, true, saveState(ctx, statePath, state)
}

func saveState(ctx context.Context, statePath string, state map[string]entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(statePath), ".signals-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	err = json.NewEncoder(f).Encode(state)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), statePath)
}
