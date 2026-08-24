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

// activeSpinners counts the spinners currently animating stderr. A warning
// printed while one is running lands mid-frame, so CliWarning breaks to a new
// line first—but only then, since off a TTY there is no frame to step off.
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
		interval: 100 * time.Millisecond,
		enabled:  term.IsTerminal(int(os.Stderr.Fd())),
	}
}

// Start begins the spinner animation
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
	s.mu.Unlock()

	if !s.enabled {
		// Print a static message so users still see progress in non-TTY output.
		// Deliberately printed again on every restart: a long approval wait
		// restarts the spinner each poll tick, and the repeated line is the
		// heartbeat a redirected log gets from an otherwise silent wait.
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

			// Animate dots if message ends with "..."
			displayMsg := msg
			if baseMsg, ok := strings.CutSuffix(msg, "..."); ok {
				displayMsg = baseMsg + dots[dotIdx%len(dots)]
			}

			fmt.Fprintf(os.Stderr, "\r%s %s", Yellow(frame), displayMsg)
			frameIdx++

			// Update dots every 3 frames (300ms)
			dotCounter++
			if dotCounter >= 3 {
				dotIdx++
				dotCounter = 0
			}

			// Blocking here instead of sleeping lets Stop return as soon as it
			// closes stopCh rather than up to a frame later.
			select {
			case <-stopCh:
				// Clear the spinner line
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop stops the spinner animation
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

// StopWriter wraps w so the spinner stops before the first byte written to it
// reaches the terminal. A spinner animates the line a caller is about to write
// to and Stop erases that line, so output streamed while one is running gets
// drawn over and then wiped. Callers that wait on something which may start
// producing output—an approved command resuming, an operation retried after
// MFA—hand their writer through here instead of having to know when the first
// byte arrives. An empty write is not output, so it leaves the spinner alone.
func (s *Spinner) StopWriter(w io.Writer) io.Writer {
	return &spinnerStopWriter{spinner: s, w: w}
}

func (w *spinnerStopWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.once.Do(w.spinner.Stop)
	}
	return w.w.Write(p)
}
