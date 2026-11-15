package utils

import (
	"os"
	"os/exec"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: observes the process-wide refresh tracker.
func TestWaitForTokenRefreshes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		delay, want time.Duration
	}{
		{"finishes before exit", time.Second, time.Second},
		{"bounded wait", 20 * time.Second, 19 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				finish := BeginTokenRefresh()
				done := make(chan struct{})
				go func() { time.Sleep(tc.delay); finish(); close(done) }()
				start := time.Now()
				WaitForTokenRefreshes()
				assert.Equal(t, tc.want, time.Since(start))
				<-done
			})
		})
	}
}

// Serial: each child exits while a refresh is saving.
func TestExitPathsWaitForTokenRefresh(t *testing.T) {
	for _, mode := range []string{"direct", "error", "envelope", "json"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRefreshExitHelperProcess$")
			cmd.Env = append(os.Environ(), "ALPACON_TEST_REFRESH_EXIT="+mode)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 6, exitErr.ExitCode())
			assert.Contains(t, string(out), "rotated token saved")
		})
	}
}

// Serial: only the child reaches the exit under test.
func TestRefreshExitHelperProcess(t *testing.T) {
	mode := os.Getenv("ALPACON_TEST_REFRESH_EXIT")
	if mode == "" {
		return
	}
	synctest.Test(t, func(t *testing.T) {
		finish := BeginTokenRefresh()
		go func() {
			time.Sleep(time.Second)
			_, _ = os.Stdout.WriteString("rotated token saved\n")
			finish()
		}()
		switch mode {
		case "direct":
			Exit(6)
		case "error":
			exitOnError(NewExitErrorWithCode(6, "refused"))
		case "envelope":
			CliErrorEnvelopeWithExitCode(6, "", nil, "refused")
		case "json":
			OutputFormat = OutputFormatJSON
			ExitWithFailureJSON(Failure{ExitCode: 6, Message: "refused"})
		}
	})
}
