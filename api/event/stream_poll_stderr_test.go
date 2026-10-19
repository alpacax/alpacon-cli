package event

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestStderrGate_CloseWaitsOutAWriteInFlight(t *testing.T) {
	t.Parallel()
	g := &stderrGate{}
	entered, release := make(chan struct{}), make(chan struct{})
	var wrote atomic.Bool
	go g.do(func() {
		close(entered)
		<-release
		wrote.Store(true)
	})
	<-entered

	closed := make(chan struct{})
	go func() {
		g.close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("close returned while a write was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-closed
	assert.True(t, wrote.Load(), "the in-flight write must finish before close returns")
}

func TestStderrGate_WriteAfterCloseIsDropped(t *testing.T) {
	t.Parallel()
	g := &stderrGate{}
	g.close()

	ran := false
	g.do(func() { ran = true })

	assert.False(t, ran)
}

func lateThrottleNotice(t *testing.T, format string) string {
	t.Helper()
	inFlight, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if once.CompareAndSwap(false, true) {
			close(inFlight)
		}
		<-release
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	prev := utils.OutputFormat
	utils.OutputFormat = format
	t.Cleanup(func() { utils.OutputFormat = prev })

	_, stderr := testutil.CaptureOutput(t, func() {
		p := startStreamPoll(ac, "cmd-1", time.Minute, time.Millisecond, false)
		<-inFlight

		stopped := make(chan struct{})
		go func() {
			p.stop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("stop waited on a request in flight")
		}

		close(release)
		select {
		case <-p.exited:
		case <-time.After(10 * time.Second):
			t.Error("the poll never exited")
		}
	})
	return stderr
}

// Serial: swaps os.Stderr and utils.OutputFormat.
func TestStreamPoll_JSONModeStopSilencesAThrottleNoticeThatLandsAfterIt(t *testing.T) {
	assert.Empty(t, lateThrottleNotice(t, utils.OutputFormatJSON))
}

// Serial: swaps os.Stderr and utils.OutputFormat.
func TestStreamPoll_TableModeStillPrintsALateThrottleNotice(t *testing.T) {
	assert.NotEmpty(t, lateThrottleNotice(t, utils.OutputFormatTable))
}
