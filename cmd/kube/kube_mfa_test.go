package kube

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMFALink   = "https://my-workspace.example.com/api/auth0/cli-pkce/start/?flow=abc"
	testMFATick   = 10 * time.Millisecond
	testMFAWindow = 100*testMFATick + testMFATick/2
)

// mfaServer answers the MFA link and completion endpoints and counts what each got.
type mfaServer struct {
	mu          sync.Mutex
	link        func(w http.ResponseWriter)
	completions []func(w http.ResponseWriter) // in turn, repeating the last
	linkHits    int
	polls       int
	query       string
}

func (s *mfaServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth0/mfa/":
			s.linkHits++
			s.query = r.URL.RawQuery
			s.link(w)
		case "/api/auth0/mfa/completion/":
			reply := s.completions[min(s.polls, len(s.completions)-1)]
			s.polls++
			reply(w)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
}

func (s *mfaServer) counts() (linkHits, polls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linkHits, s.polls
}

func reply(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

var (
	linkIssued  = reply(http.StatusOK, `{"mfa_url": "`+testMFALink+`"}`)
	alreadyDone = reply(http.StatusOK, `{"detail": "MFA is already done."}`)
	notYet      = reply(http.StatusOK, `{"completed": false}`)
	done        = reply(http.StatusOK, `{"completed": true}`)
)

// mfaClient returns a bearer client served by srv in memory, so the command runs inside a
// synctest bubble.
func mfaClient(t *testing.T, srv *mfaServer) *client.AlpaconClient {
	t.Helper()
	ac := &client.AlpaconClient{
		HTTPClient:    testutil.HandlerClient(srv.handler(t)),
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
	}
	ac.SetAccessToken("access-token")
	return ac
}

// mfaRun is what one run of the command saw: the browser opens it asked for and its streams.
type mfaRun struct {
	opened         []string
	stdout, stderr string
	code           int
}

// mfaMode is how a run is invoked: from a terminal or not, under --output json or not, and
// whether stderr is a terminal a person reads.
type mfaMode struct {
	interactive, json, stderrTerminal bool
}

var (
	ttyTable      = mfaMode{interactive: true, stderrTerminal: true}
	ttyJSON       = mfaMode{interactive: true, json: true, stderrTerminal: true}
	ttyJSONPiped  = mfaMode{interactive: true, json: true}
	ttyTablePiped = mfaMode{interactive: true}
	scriptTable   = mfaMode{}
	scriptJSON    = mfaMode{json: true}
)

func runKubeMFACmd(ac *client.AlpaconClient, mode mfaMode, args ...string) *mfaRun {
	run := &mfaRun{}
	build := func(ios *utils.IOStreams) *cobra.Command {
		return newKubeMFACmdWith(func() (*client.AlpaconClient, error) { return ac, nil }, ios, kubeMFAOptions{
			interactive:    func() bool { return mode.interactive },
			stderrTerminal: func() bool { return mode.stderrTerminal },
			open:           func(url string) { run.opened = append(run.opened, url) },
			tick:           testMFATick,
			timeout:        testMFAWindow,
		})
	}
	if mode.json {
		run.stdout, run.stderr, run.code = cmdtest.RunJSON(build, args...)
	} else {
		run.stdout, run.stderr, run.code = cmdtest.RunCaptured(build, args...)
	}
	return run
}

// envelopeAfterTheLink decodes the envelope a waiting run ends stderr with, after the link line
// it printed for the person at the terminal.
func envelopeAfterTheLink(t *testing.T, stderr string) utils.JSONErrorEnvelope[map[string]any] {
	t.Helper()
	at := strings.Index(stderr, "{")
	require.Positive(t, at, stderr)
	assert.Contains(t, stderr[:at], testMFALink)
	return envelopeOf(t, stderr[at:])
}

// envelopeOf decodes stderr as the one error envelope it must be.
func envelopeOf(t *testing.T, stderr string) utils.JSONErrorEnvelope[map[string]any] {
	t.Helper()
	var env utils.JSONErrorEnvelope[map[string]any]
	dec := json.NewDecoder(strings.NewReader(stderr))
	require.NoError(t, dec.Decode(&env), stderr)
	assert.False(t, dec.More(), "stderr must hold only the envelope: %q", stderr)
	return env
}

func TestKubeMFA_ConfirmsAfterTheBrowserStep(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode mfaMode
	}{
		{name: "table", mode: ttyTable},
		{name: "json", mode: ttyJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: linkIssued, completions: []func(http.ResponseWriter){notYet, notYet, done}}
			var run *mfaRun
			synctest.Test(t, func(t *testing.T) {
				run = runKubeMFACmd(mfaClient(t, srv), tt.mode)
			})

			require.Equal(t, 0, run.code, run.stderr)
			assert.Equal(t, []string{testMFALink}, run.opened)
			_, polls := srv.counts()
			assert.Equal(t, 3, polls)
			assert.Contains(t, srv.query, "location=cli")
			assert.Contains(t, srv.query, "workspace=my-workspace")
			assert.NotContains(t, srv.query, "server=", "a kube session names no server")
			if tt.mode.json {
				assert.JSONEq(t, `{"workspace": "my-workspace", "verified": true, "already_verified": false}`, run.stdout)
				assert.Contains(t, run.stderr, testMFALink, "the person needs the link even when the browser opens")
				return
			}
			assert.Contains(t, run.stderr, `Kubernetes writes and deletes in workspace "my-workspace" need a recent MFA. Open this link to verify:
  `+testMFALink+"\n")
			assert.Empty(t, run.stdout)
			assert.Contains(t, run.stderr, `MFA verified for this CLI in workspace "my-workspace".`)
			assert.Contains(t, run.stderr, "Retry the Kubernetes request.")
			assert.Contains(t, run.stderr, "run 'alpacon kube login' for the cluster again", "a session tied to another login needs a way out")
		})
	}
}

// The server answers with no link while the client's MFA is still fresh. One completion check
// confirms it before success is reported, and nothing is opened or waited on.
func TestKubeMFA_AlreadyFreshIssuesNoLink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode mfaMode
	}{
		{name: "interactive_table", mode: ttyTable},
		{name: "non_interactive_json", mode: scriptJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: alreadyDone, completions: []func(http.ResponseWriter){done}}
			run := runKubeMFACmd(mfaClient(t, srv), tt.mode)

			require.Equal(t, 0, run.code, run.stderr)
			assert.Empty(t, run.opened)
			linkHits, polls := srv.counts()
			assert.Equal(t, 1, linkHits)
			assert.Equal(t, 1, polls)
			if tt.mode.json {
				assert.JSONEq(t, `{"workspace": "my-workspace", "verified": true, "already_verified": true}`, run.stdout)
				assert.Empty(t, run.stderr)
				return
			}
			assert.Contains(t, run.stderr, `MFA is already verified for this CLI in workspace "my-workspace".`)
			assert.NotContains(t, run.stderr, "Open this link")
		})
	}
}

// A 200 with no link is not proof of a fresh MFA by itself: when the completion check does not
// confirm it, the command fails rather than report an MFA it never saw.
func TestKubeMFA_NoLinkWithoutAFreshMFAExitsOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		completion func(w http.ResponseWriter)
		want       string
	}{
		{name: "not_completed", completion: notYet, want: "The server issued no MFA link and does not report the MFA as done"},
		{name: "check_failed", completion: reply(http.StatusServiceUnavailable, `{}`), want: "Failed to confirm the MFA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: reply(http.StatusOK, `{}`), completions: []func(http.ResponseWriter){tt.completion}}
			run := runKubeMFACmd(mfaClient(t, srv), ttyTable)

			assert.Equal(t, utils.ExitCodeGeneralError, run.code)
			assert.Empty(t, run.stdout)
			assert.Contains(t, run.stderr, tt.want)
			assert.NotContains(t, run.stderr, "verified for this CLI")
			assert.Empty(t, run.opened)
			_, polls := srv.counts()
			assert.Equal(t, 1, polls)
		})
	}
}

// Where nobody can see the link while it waits, stdin or stderr not a terminal, the command
// hands out the link and exits 9 at once; the browser leg credits the MFA whether or not the
// CLI is still polling.
func TestKubeMFA_NonInteractiveHandsOutTheLinkAndExitsNine(t *testing.T) {
	t.Parallel()
	tableTests := []struct {
		name string
		mode mfaMode
	}{
		{name: "no_terminal", mode: scriptTable},
		{name: "stderr_redirected", mode: ttyTablePiped},
	}
	for _, tt := range tableTests {
		t.Run("table_"+tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: linkIssued, completions: []func(http.ResponseWriter){done}}
			run := runKubeMFACmd(mfaClient(t, srv), tt.mode)

			assert.Equal(t, utils.ExitCodeMFARequired, run.code)
			assert.Empty(t, run.stdout)
			_, polls := srv.counts()
			assert.Zero(t, polls, "a run nobody watches must not wait")
			assert.Empty(t, run.opened, "a script on a desktop must not raise a browser")
			assert.Equal(t, `Warning: Kubernetes writes and deletes in workspace "my-workspace" need a recent MFA, and it is not verified yet.
Next:
  Open this link in a browser and finish MFA: `+testMFALink+`
  alpacon kube mfa  # run it again once MFA is done to confirm, then retry the Kubernetes request
`, run.stderr)
		})
	}

	jsonTests := []struct {
		name string
		mode mfaMode
	}{
		{name: "no_terminal", mode: scriptJSON},
		// A person at the terminal would not see a link written to a redirected stderr.
		{name: "stderr_redirected", mode: ttyJSONPiped},
	}
	for _, tt := range jsonTests {
		t.Run("json_"+tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: linkIssued, completions: []func(http.ResponseWriter){done}}
			run := runKubeMFACmd(mfaClient(t, srv), tt.mode)

			assert.Equal(t, utils.ExitCodeMFARequired, run.code)
			assert.Empty(t, run.stdout)
			assert.Empty(t, run.opened)
			_, polls := srv.counts()
			assert.Zero(t, polls)
			env := envelopeOf(t, run.stderr)
			assert.Equal(t, utils.ExitCodeMFARequired, env.ExitCode)
			assert.Equal(t, utils.ErrorCodeMFARequired, env.ErrorCode)
			assert.Equal(t, `Kubernetes writes and deletes in workspace "my-workspace" need a recent MFA, and it is not verified yet.`, env.Message)
			assert.Equal(t, opKubeMFA, env.Context["operation"])
			assert.Equal(t, []utils.NextAction{
				{Description: "Open this link in a browser and finish MFA: " + testMFALink},
				{Command: "alpacon kube mfa", Description: "run it again once MFA is done to confirm, then retry the Kubernetes request"},
			}, env.NextActions)
		})
	}
}

func TestKubeMFA_TimeoutExitsNine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode mfaMode
	}{
		{name: "table", mode: ttyTable},
		{name: "json", mode: ttyJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: linkIssued, completions: []func(http.ResponseWriter){notYet}}
			var run *mfaRun
			synctest.Test(t, func(t *testing.T) {
				run = runKubeMFACmd(mfaClient(t, srv), tt.mode)
			})

			assert.Equal(t, utils.ExitCodeMFARequired, run.code)
			assert.Empty(t, run.stdout)
			_, polls := srv.counts()
			assert.Equal(t, 100, polls)
			if !tt.mode.json {
				assert.Contains(t, run.stderr, "Warning: MFA was not verified within 1.005s.\n")
				assert.Contains(t, run.stderr, "  alpacon kube mfa  # ")
				return
			}
			env := envelopeAfterTheLink(t, run.stderr)
			assert.Equal(t, utils.ErrorCodeMFARequired, env.ErrorCode)
			assert.Equal(t, "MFA was not verified within 1.005s.", env.Message)
			require.Len(t, env.NextActions, 2)
			assert.Equal(t, "alpacon kube mfa", env.NextActions[1].Command)
		})
	}
}

// A refusal on a completion check ends the wait as a failure, not as a timeout: no later poll
// can change it.
func TestKubeMFA_RefusalDuringTheWaitExitsOne(t *testing.T) {
	t.Parallel()
	srv := &mfaServer{link: linkIssued, completions: []func(http.ResponseWriter){
		notYet, reply(http.StatusUnauthorized, `{"code": "auth_client_instance_revoked"}`),
	}}
	var run *mfaRun
	synctest.Test(t, func(t *testing.T) {
		run = runKubeMFACmd(mfaClient(t, srv), ttyJSON)
	})

	assert.Equal(t, utils.ExitCodeGeneralError, run.code)
	_, polls := srv.counts()
	assert.Equal(t, 2, polls)
	env := envelopeAfterTheLink(t, run.stderr)
	assert.Equal(t, "auth_client_instance_revoked", env.ErrorCode)
	assert.Contains(t, env.Message, "Failed to confirm the MFA")
}

func TestKubeMFA_FailuresBeforeTheWait(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		bearer    bool
		link      func(w http.ResponseWriter)
		args      []string
		wantCode  int
		wantError string
		wantLinks int
	}{
		{
			name:      "token_login",
			link:      linkIssued,
			wantCode:  utils.ExitCodeGeneralError,
			wantError: "Tokens issued by Alpacon (service or personal API token) cannot confirm MFA; it needs 'alpacon login' without -t",
		},
		{
			name:      "link_refused",
			bearer:    true,
			link:      reply(http.StatusBadRequest, `{"code": "workspace_not_found"}`),
			wantCode:  utils.ExitCodeGeneralError,
			wantError: "Failed to get the MFA link",
			wantLinks: 1,
		},
		{
			name:      "an_argument",
			bearer:    true,
			link:      linkIssued,
			args:      []string{"prod-east"},
			wantCode:  utils.ExitCodeUsageError,
			wantError: `unknown command "prod-east"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &mfaServer{link: tt.link, completions: []func(http.ResponseWriter){done}}
			ac := mfaClient(t, srv)
			if !tt.bearer {
				ac.SetAccessToken("")
				ac.Token = "api-token"
			}
			run := runKubeMFACmd(ac, ttyTable, tt.args...)

			assert.Equal(t, tt.wantCode, run.code, run.stderr)
			assert.Contains(t, run.stderr, tt.wantError)
			assert.Empty(t, run.opened)
			linkHits, polls := srv.counts()
			assert.Equal(t, tt.wantLinks, linkHits)
			assert.Zero(t, polls)
		})
	}
}

func TestKubeMFA_NoLoginExitsOne(t *testing.T) {
	t.Parallel()
	_, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command {
		return newKubeMFACmd(cmdtest.NoClient, ios)
	})

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, stderr, "Connection to Alpacon API failed")
}
