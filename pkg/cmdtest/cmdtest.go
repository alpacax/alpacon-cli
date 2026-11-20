// Package cmdtest runs a leaf command in process against a test server, as cmd.Execute would.
// It sits apart from pkg/testutil, which client's own tests import, because it builds a client.
package cmdtest

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
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
	ios, _, _ := utils.TestIOStreams() // formats the failure without touching the global format
	_ = ios.SetOutputFormat(utils.OutputFormat)
	var out strings.Builder
	code = run(cmd, args, ios, &out, utils.OutputFormatIn(args))
	return out.String(), code
}

// Build constructs the command under test on the streams it is handed.
type Build func(ios *utils.IOStreams) *cobra.Command

// RunCaptured runs the command build returns on test streams and returns what it wrote to
// each; the failure is appended to stderr. It swaps nothing, so it is safe under t.Parallel.
func RunCaptured(build Build, args ...string) (stdout, stderr string, code int) {
	ios, out, errOut := utils.TestIOStreams()
	return runOn(ios, out, errOut, build, args)
}

// RunJSON is RunCaptured under --output json.
func RunJSON(build Build, args ...string) (stdout, stderr string, code int) {
	ios, out, errOut := utils.TestIOStreams()
	_ = ios.SetOutputFormat(utils.OutputFormatJSON)
	return runOn(ios, out, errOut, build, args)
}

func runOn(ios *utils.IOStreams, out, errOut *bytes.Buffer, build Build, args []string) (string, string, int) {
	cmd := build(ios)
	cmd.SetOut(ios.Out) // Cobra's own output, help included
	cmd.SetErr(ios.ErrOut)
	code := run(cmd, args, ios, errOut, utils.OutputFormatIn(args))
	return out.String(), errOut.String(), code
}

// run executes cmd as cmd.Execute does and prints its failure to w in ios's format, turning an
// error that is not an ExitError into the one cmd.Execute prints. A parse error applies format,
// the --output its arguments carry, as cmd.Execute does.
func run(cmd *cobra.Command, args []string, ios *utils.IOStreams, w io.Writer, format string) int {
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as the root is
	cmd.SetArgs(append([]string{}, args...))         // never nil, or Cobra reads os.Args[1:]
	restore := utils.MarkHookErrors(cmd)             // as cmd.Execute does, to tell a hook's failure from a parse error
	failed, err := cmd.ExecuteC()
	restore()
	if err == nil {
		return 0
	}
	exitErr, ok := errors.AsType[*utils.ExitError](err)
	switch {
	case ok:
	case !utils.IsHookError(err):
		exitErr, _ = errors.AsType[*utils.ExitError](ios.NewParseExitError(failed, format, err.Error()))
	case utils.IsUsageError(err):
		exitErr, _ = errors.AsType[*utils.ExitError](utils.NewUsageExitError("%s", err))
	default:
		exitErr, _ = errors.AsType[*utils.ExitError](utils.NewExitError("While executing the command: %s", err))
	}
	ios.FprintExitError(w, exitErr)
	return exitErr.Code
}
