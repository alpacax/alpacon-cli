package utils

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

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

// NewSpinner creates a new spinner with the given message
// If the message ends with "...", the dots will animate (. -> .. -> ...)
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message:  message,
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		dots:     []string{".  ", ".. ", "..."},
		interval: 100 * time.Millisecond,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
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
	s.mu.Unlock()

	if !s.enabled {
		// Print a static message so users still see progress in non-TTY output
		fmt.Fprintln(os.Stderr, s.message)
		return
	}

	go func() {
		defer close(s.doneCh)
		frameIdx := 0
		dotIdx := 0
		dotCounter := 0
		for {
			select {
			case <-s.stopCh:
				// Clear the spinner line
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			default:
				s.mu.Lock()
				msg := s.message
				s.mu.Unlock()

				frame := s.frames[frameIdx%len(s.frames)]

				// Animate dots if message ends with "..."
				displayMsg := msg
				if strings.HasSuffix(msg, "...") {
					baseMsg := strings.TrimSuffix(msg, "...")
					displayMsg = baseMsg + s.dots[dotIdx%len(s.dots)]
				}

				fmt.Fprintf(os.Stderr, "\r%s %s", Yellow(frame), displayMsg)
				frameIdx++

				// Update dots every 3 frames (300ms)
				dotCounter++
				if dotCounter >= 3 {
					dotIdx++
					dotCounter = 0
				}

				time.Sleep(s.interval)
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
	s.mu.Unlock()

	if !s.enabled {
		return
	}

	close(s.stopCh)
	<-s.doneCh
}

// StopWithMessage stops the spinner and prints a final message
func (s *Spinner) StopWithMessage(message string) {
	s.Stop()
	fmt.Fprintln(os.Stderr, message)
}

// UpdateMessage updates the spinner message while running
func (s *Spinner) UpdateMessage(message string) {
	s.mu.Lock()
	s.message = message
	s.mu.Unlock()
}
