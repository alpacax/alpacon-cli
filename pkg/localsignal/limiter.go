package localsignal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
)

type entry struct {
	Last   time.Time `json:"last"`
	Folded int       `json:"folded"`
}

func allow(statePath, session string, kind Kind, clock func() time.Time) (folded int, emit bool, err error) {
	// Hold a sibling lock: renaming the state file replaces its inode.
	var lock *filelock.Lock
	for {
		lock, err = filelock.Acquire(statePath+".lock", 0600)
		if !errors.Is(err, filelock.ErrLocked) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		return 0, true, err
	}
	defer func() { _ = lock.Release() }()
	now := clock()
	state := map[string]entry{}
	if data, readErr := os.ReadFile(statePath); readErr == nil {
		if json.Unmarshal(data, &state) != nil || state == nil {
			state = map[string]entry{}
		}
	}
	key := string(kind)
	if session != "" {
		key += ":" + session
	}
	last, found := state[key]
	if found && now.Sub(last.Last) < 60*time.Second {
		last.Folded++
		state[key] = last
		err := saveState(statePath, state)
		return 0, err != nil, err
	}
	state[key] = entry{Last: now}
	return last.Folded, true, saveState(statePath, state)
}

func saveState(statePath string, state map[string]entry) error {
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
	return os.Rename(f.Name(), statePath)
}
