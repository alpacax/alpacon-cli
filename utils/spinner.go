package utils

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

// defaultSpinnerInterval is the frame period NewSpinner hands out, and the
// fallback Start uses when the field holds something a ticker cannot take.
const defaultSpinnerInterval = 100 * time.Millisecond

// activeSpinners counts the spinners animating stderr, so CliWarning and CliDebug break to a new
// line before printing only while one is mid-frame.
var activeSpinners atomic.Int32

// Spinner displays an animated spinner with a message.
// When stderr is not a terminal (e.g., piped or redirected), the spinner
// animation is replaced with a single static progress line so logs stay
// clean without ANSI artifacts.
type Spinner struct {
	message  string
	frames   []string
	dots     []string
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	mu       sync.Mutex
	running  bool
	enabled  bool
}

// spinnerStopWriter retires a spinner before the first byte of a caller's output
// lands. See Spinner.StopWriter.
type spinnerStopWriter struct {
	spinner *Spinner
	w       io.Writer
	once    sync.Once
}

// NewSpinner creates a new spinner with the given message
// If the message ends with "...", the dots will animate (. -> .. -> ...)
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message:  message,
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		dots:     []string{".  ", ".. ", "..."},
		interval: defaultSpinnerInterval,
		enabled:  term.IsTerminal(int(os.Stderr.Fd())),
	}
}

// Start begins the spinner animation.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	// Stop closes both channels, so a restart needs a fresh pair—and the
	// goroutine below takes its own copy, since the fields belong to whichever
	// Start ran last rather than to the run it was spawned for.
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	s.stopCh, s.doneCh = stopCh, doneCh
	// The rest of what the goroutine needs is copied in the same critical
	// section, so it never reaches back into the struct during its run.
	msg := s.message
	frames, dots := s.frames, s.dots
	interval := s.interval
	if interval <= 0 {
		// time.NewTicker panics on a non-positive period, and a stderr animation must never take
		// the CLI down with it.
		interval = defaultSpinnerInterval
	}
	s.mu.Unlock()

	if !s.enabled {
		// Off a TTY, print the message as one static line per Start. No caller restarts it on a
		// timer, so a long wait shows one line in a redirected log rather than a heartbeat.
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	activeSpinners.Add(1)

	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		frameIdx := 0
		dotIdx := 0
		dotCounter := 0
		// Drawing before the first stopCh check is deliberate: a Start answered
		// at once paints one frame and erases it, and Stop waits on doneCh, so
		// both land before the caller's own output.
		for {
			frame := frames[frameIdx%len(frames)]

			displayMsg := msg
			if baseMsg, ok := strings.CutSuffix(msg, "..."); ok {
				displayMsg = baseMsg + dots[dotIdx%len(dots)]
			}

			fmt.Fprintf(os.Stderr, "\r%s %s", Yellow(frame), displayMsg)
			frameIdx++

			dotCounter++
			if dotCounter >= 3 {
				dotIdx++
				dotCounter = 0
			}

			// Blocking here instead of sleeping lets Stop return as soon as it
			// closes stopCh rather than up to a frame later.
			select {
			case <-stopCh:
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop stops the spinner animation.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	// Read under the lock: a concurrent restart replaces the fields.
	stopCh, doneCh := s.stopCh, s.doneCh
	s.mu.Unlock()

	if !s.enabled {
		return
	}

	close(stopCh)
	<-doneCh
	// After the goroutine has returned, so the window where the last frame is
	// still on screen keeps its line break.
	activeSpinners.Add(-1)
}

// StopWriter wraps w so the spinner stops before the first byte written to it reaches the
// terminal; otherwise output streamed under a running spinner is drawn over, then erased by Stop.
// Callers waiting on something that may start producing output hand their writer through here. An
// empty write leaves the spinner alone.
func (s *Spinner) StopWriter(w io.Writer) io.Writer {
	return &spinnerStopWriter{spinner: s, w: w}
}

func (w *spinnerStopWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.once.Do(w.spinner.Stop)
	}
	return w.w.Write(p)
}
