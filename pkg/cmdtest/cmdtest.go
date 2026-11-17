// Package cmdtest runs a leaf command in process against a test server, as cmd.Execute would.
// It sits apart from pkg/testutil, which client's own tests import, because it builds a client.
package cmdtest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// Reply is the status and JSON body a Server answers a route with.
type Reply struct {
	Status int
	Body   string
}

// Fake is a test server that answers fixed routes and counts the requests each one got.
type Fake struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
}

// Server answers each "METHOD /path" key of routes with its Reply, ignoring the
// query string, and fails the test on any other request.
func Server(t *testing.T, routes map[string]Reply) *Fake {
	t.Helper()
	f := &Fake{hits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.Method + " " + r.URL.Path
		reply, ok := routes[route]
		if !ok {
			t.Errorf("unexpected request: %s", route)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		f.hits[route]++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		_, _ = w.Write([]byte(reply.Body))
	}))
	t.Cleanup(f.Close)
	return f
}

// Hits returns how many requests route ("METHOD /path") got.
func (f *Fake) Hits(route string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[route]
}

// Factory returns a client factory aimed at f.
func (f *Fake) Factory() func() (*client.AlpaconClient, error) { return ClientFactory(f.Server) }

// ClientFactory returns a client factory aimed at ts, so a test needs no login in HOME.
func ClientFactory(ts *httptest.Server) func() (*client.AlpaconClient, error) {
	return func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, UserAgent: "alpacon-test"}, nil
	}
}

// ErrNoLogin is what NoClient fails with.
var ErrNoLogin = errors.New("not logged in")

// NoClient fails as a missing login does.
func NoClient() (*client.AlpaconClient, error) { return nil, ErrNoLogin }

// Run executes cmd with args and returns the failure cmd.Execute would print and
// its exit code. It captures nothing else, so it is safe under t.Parallel.
func Run(cmd *cobra.Command, args ...string) (failure string, code int) {
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as RootCmd is
	cmd.SetArgs(append([]string{}, args...))         // never nil, or Cobra reads os.Args[1:]
	restore := utils.MarkHookErrors(cmd)             // as cmd.Execute does, to tell a hook's failure from a parse error
	err := cmd.Execute()
	restore()
	if err == nil {
		return "", 0
	}
	var out strings.Builder
	if exitErr, ok := errors.AsType[*utils.ExitError](err); ok {
		utils.FprintExitError(&out, exitErr)
		return out.String(), exitErr.Code
	}
	if utils.IsHookError(err) && !utils.IsUsageError(err) {
		return err.Error(), utils.ExitCodeGeneralError
	}
	return err.Error(), utils.ExitCodeUsageError // Cobra's own parse error, or a hook's usage error
}

// RunCaptured is Run with os.Stdout and os.Stderr captured; the failure is
// appended to stderr. Serial: it swaps both.
func RunCaptured(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var failure string
	stdout, stderr = testutil.CaptureOutput(t, func() { failure, code = Run(cmd, args...) })
	return stdout, stderr + failure, code
}

// RunJSON is RunCaptured under --output json. Serial: it also sets utils.OutputFormat.
func RunJSON(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	old := utils.OutputFormat
	utils.OutputFormat = utils.OutputFormatJSON
	defer func() { utils.OutputFormat = old }()
	return RunCaptured(t, cmd, args...)
}
