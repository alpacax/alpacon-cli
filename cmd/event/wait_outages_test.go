package event

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type warnings struct {
	mu   sync.Mutex
	list []string
}

func (w *warnings) warn(cause error, reconnect bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if reconnect {
		w.list = append(w.list, "reconnect: "+cause.Error())
		return
	}
	w.list = append(w.list, "catch-up: "+cause.Error())
}

func TestOutageReporterReportsUntilStoppedAndNeverAfter(t *testing.T) {
	t.Parallel()
	catchUp, reconnect := make(chan error), make(chan error, 1)
	got := &warnings{}
	stop := startOutageReporter(catchUp, reconnect, got.warn)

	catchUp <- errors.New("first")
	stop()
	reconnect <- errors.New("late")

	assert.Equal(t, []string{"catch-up: first"}, got.list)
	assert.Len(t, reconnect, 1, "a notice queued after stop must stay unread")
}

func TestOutageReporterStopIsIdempotent(t *testing.T) {
	t.Parallel()
	stop := startOutageReporter(make(chan error), make(chan error), (&warnings{}).warn)

	stop()
	stop()
}
