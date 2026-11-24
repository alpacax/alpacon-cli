package worksession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type selectionCodedError struct{ code string }

func (e selectionCodedError) Error() string     { return e.code }
func (e selectionCodedError) ErrorCode() string { return e.code }
func (selectionCodedError) ErrorSource() string { return "server" }

func TestIneligibleSelectionKeepsDiagnosticAndSendsNoLookup(t *testing.T) {
	t.Parallel()
	refusal := selectionCodedError{code: utils.WorkSessionRequired}
	const baseline = `Error: the command operation requires an active WorkSession on this authentication.

  auth          : Browser login (interactive)
  reason        : no WorkSession selected for this shell
  required scope: command
  target server : web-1

Next:
  alpacon work-session ls --status active  # find an existing active session; AI agent: reuse it by prefixing the gated command with --work-session <ID>
  alpacon work-session use <ID>  # human: attach an existing session (rejects agent sessions)
  alpacon work-session create --scope command --server web-1 --expires-in 1h --purpose "<intent>" --use  # none active? create a new one (human)
  alpacon work-session create --scope command --server web-1 --expires-in 1h --purpose "<intent>" --requester-type agent  # none active? create a new one (AI agent; prefix the gated command with --work-session <ID>)

Note: Browser login and personal API tokens require an active, approved WorkSession. Service tokens use endpoint-specific authorization.
`
	tests := []struct {
		name   string
		env    offerEnv
		source Source
	}{
		{name: "non interactive", env: offerEnv{stderrTTY: true, browserLogin: true}},
		{name: "stderr redirected", env: offerEnv{stdinTTY: true, browserLogin: true}},
		{name: "agent detected", env: offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true, agent: true}},
		{name: "JSON output", env: offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true, json: true}},
		{name: "token authentication", env: offerEnv{stdinTTY: true, stderrTTY: true}},
		{name: "prompt opt out", env: offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true, optOut: true}},
		{name: "flag source", env: offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true}, source: SourceFlag},
		{name: "environment source", env: offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true}, source: SourceEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
				requests.Add(1)
				return http.StatusInternalServerError, `{}`
			})}
			ios, _, _ := utils.TestIOStreams()
			selection := &Selection{canOffer: offerAllowed(tt.env), source: tt.source, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: ac}
			callbacks := selection.Offer(utils.ErrorHandlerCallbacks{RetryOperation: func() error {
				t.Error("ineligible selection retried the operation")
				return nil
			}})
			assert.Nil(t, callbacks.OnWorkSessionRefused)

			got := utils.HandleCommonErrors(utils.MarkSubmission(refusal), "web-1", callbacks)

			assert.Equal(t, refusal, got)
			assert.Equal(t, baseline, printedWorkSessionFailure(t, got))
			assert.Zero(t, requests.Load())
		})
	}
}

func printedWorkSessionFailure(t *testing.T, err error) string {
	t.Helper()
	exitErr, ok := errors.AsType[*utils.ExitError](utils.WorkSessionExitError(err, utils.OperationCommand, "web-1", "Browser login", ""))
	require.True(t, ok)
	ios, _, stderr := utils.TestIOStreams()
	ios.PrintExitError(exitErr)
	return stderr.String()
}

func TestAcceptedReusableSessionIsSavedWithoutCreatingAnother(t *testing.T) {
	setupTmpConfig(t)
	read := replaceStdin(t, "y\n")
	defer func() { _ = read.Close() }()
	now := time.Now().UTC()
	candidate := activeServerSession("reuse-session", now)
	var creates atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			assert.Equal(t, "active,approved,pending", r.URL.Query().Get("status"))
			assert.Equal(t, "user", r.URL.Query().Get("requester_type"))
			assert.Equal(t, "user-1", r.URL.Query().Get("assigned_user"))
			assert.Empty(t, r.URL.Query().Get("cluster"))
			assert.Equal(t, "server-1", r.URL.Query().Get("server"))
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []wsapi.WorkSession{candidate}})
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodPost:
			creates.Add(1)
			http.Error(w, "unexpected create", http.StatusInternalServerError)
		case r.URL.Path == "/api/work-sessions/sessions/reuse-session/":
			_ = json.NewEncoder(w).Encode(candidate)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{canOffer: true, source: SourceNone, need: Need{Operation: Command, Server: "web-1", Account: "root"}, ios: ios, ac: newTestClient(ts)}

	err := utils.HandleCommonErrors(utils.MarkSubmission(selectionCodedError{code: utils.WorkSessionRequired}), "web-1", selection.Offer(utils.ErrorHandlerCallbacks{RetryOperation: func() error { return nil }}))

	require.NoError(t, err)
	assert.Equal(t, "reuse-session", selection.ID())
	assert.Zero(t, creates.Load())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Equal(t, "reuse-session", stored)
	assert.NotContains(t, stderr.String(), "the account the session was created for")
}

func TestOfferWaitTimeoutReturnsPendingExitAndResumeAction(t *testing.T) {
	t.Parallel()
	pending := wsapi.WorkSession{ID: "pending-session", Status: wsapi.StatusPending, RequesterType: "user", ApprovalRequestID: "approval-1"}
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		body, _ := json.Marshal(pending)
		return http.StatusOK, string(body)
	})}
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", ios: ios, ac: ac}
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}
	var resend bool
	var failure error

	synctest.Test(t, func(*testing.T) {
		resend, failure = selection.settle(context.Background(), &pending, true, want, "root")
	})

	assert.False(t, resend)
	var exitErr *utils.ExitError
	require.ErrorAs(t, failure, &exitErr)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.Code)
	assert.Contains(t, stderr.String(), "alpacon work-session use pending-session")
	assert.Contains(t, stderr.String(), "-u root")
	assert.Equal(t, "old-session", selection.ID())
}

// noInterrupt keeps signal.Notify's goroutine, which never blocks durably, out of a synctest bubble.
func noInterrupt() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func TestOfferWaitInterruptReturnsPendingExitWithoutResend(t *testing.T) {
	setupTmpConfig(t)
	pending := wsapi.WorkSession{ID: "pending-session", Status: wsapi.StatusPending, RequesterType: "user", ApprovalRequestID: "approval-1"}
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		body, _ := json.Marshal(pending)
		return http.StatusOK, string(body)
	})}
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", ios: ios, ac: ac}
	var resend bool
	var failure error
	var waited time.Duration

	synctest.Test(t, func(*testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second) // stands in for Ctrl+C
		defer cancel()
		start := time.Now()
		resend, failure = selection.settle(ctx, &pending, true, Want{}, "")
		waited = time.Since(start)
	})

	assert.False(t, resend)
	var exitErr *utils.ExitError
	require.ErrorAs(t, failure, &exitErr)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.Code)
	assert.Equal(t, time.Second, waited)
	assert.Contains(t, stderr.String(), "stopped waiting for approval")
	assert.Contains(t, stderr.String(), "alpacon work-session use pending-session")
	assert.Equal(t, "old-session", selection.ID())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.NotEqual(t, "pending-session", stored)
}

func TestOfferInterruptContextEndsOnCtrlC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send os.Interrupt to a process")
	}
	ctx, stop := (&Selection{}).interruptContext()
	defer stop()
	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)

	require.NoError(t, self.Signal(os.Interrupt))

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context outlived Ctrl+C")
	}
}

func TestUnsafeResumeIDIsRejectedBeforeRequestOrSave(t *testing.T) {
	setupTmpConfig(t)
	var requests atomic.Int32
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		requests.Add(1)
		return http.StatusOK, `{}`
	})}
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", ios: ios, ac: ac}
	unsafe := wsapi.WorkSession{ID: "safe$(touch bad)", Status: wsapi.StatusActive}

	resend, failure := selection.settle(context.Background(), &unsafe, false, Want{}, "")

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Zero(t, requests.Load())
	assert.NotContains(t, stderr.String(), unsafe.ID)
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestFreshSessionWithoutRequiredScopeIsNotSaved(t *testing.T) {
	setupTmpConfig(t)
	now := time.Now().UTC()
	initial := activeServerSession("changed-session", now)
	fresh := initial
	fresh.Scopes = []string{"webftp"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fresh)
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{id: "old-session", ios: ios, ac: newTestClient(ts)}
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}

	resend, failure := selection.settle(context.Background(), &initial, false, want, "")

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, "old-session", selection.ID())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestMissingCurrentUserIDStopsBeforeSessionList(t *testing.T) {
	t.Parallel()
	var paths bytes.Buffer
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
		paths.WriteString(r.URL.Path)
		paths.WriteByte('\n')
		return http.StatusOK, `{"id":""}`
	})}
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: ac}

	_, _, err := selection.reusableSessions(context.Background())

	require.Error(t, err)
	assert.Equal(t, "/api/iam/users/-/\n", paths.String())
}

func TestOfferAuthUsesPinnedClientCredential(t *testing.T) {
	t.Setenv(noWorkSessionPromptEnv, "disabled")
	ios, _, _ := utils.TestIOStreams()
	browserClient := &client.AlpaconClient{}
	browserClient.SetAccessToken("bearer")
	tokenClient := &client.AlpaconClient{Token: "legacy"}

	browser := readOfferEnv(ios, browserClient)
	token := readOfferEnv(ios, tokenClient)

	assert.True(t, browser.browserLogin)
	assert.False(t, token.browserLogin)
	assert.True(t, browser.optOut)
}

func TestReusePromptNamesStatusPurposeExpiryAndUncheckedAccountOnOneLine(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	starts := now.Add(time.Minute)
	session := serverSession("session-1", wsapi.StatusApproved, now)
	session.StartsAt = &starts
	session.Description = "deploy\n\x1b[31mproduction"

	prompt := reusePrompt(&session, true, "root\nignored")

	assert.NotContains(t, prompt, "\n")
	assert.NotContains(t, prompt, "\x1b")
	assert.Contains(t, prompt, "approved, not yet started")
	assert.Contains(t, prompt, "purpose: deployproduction")
	assert.Contains(t, prompt, "expires: "+session.ExpiresAt.UTC().Format(time.RFC3339))
	assert.Contains(t, prompt, "approved account cannot be checked; requested account: rootignored")

	pending := session
	pending.Status = wsapi.StatusPending
	assert.Contains(t, reusePrompt(&pending, true, ""), "pending approval")
	active := session
	active.Status = wsapi.StatusActive
	assert.Contains(t, reusePrompt(&active, false, ""), "status: active")
}

func TestOfferInvalidDurationRepromptsAndKeepsPurpose(t *testing.T) {
	setupTmpConfig(t)
	read := replaceStdin(t, "y\nrelease investigation\ninvalid\n2h\n")
	defer func() { _ = read.Close() }()
	now := time.Now().UTC()
	var created wsapi.WorkSessionCreateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodPost:
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&created)) {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(activeServerSession("new-session", now))
		case r.URL.Path == "/api/work-sessions/sessions/new-session/":
			_ = json.NewEncoder(w).Encode(activeServerSession("new-session", now))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1", Account: "root"}, ios: ios, ac: newTestClient(ts)}

	resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionExpired})

	assert.True(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, "release investigation", created.Description)
	assert.Equal(t, "02:00:00", created.Duration)
	assert.Equal(t, "root", created.TargetAccountUsername)
	assert.Contains(t, stderr.String(), "Could not use that duration")
	assert.Contains(t, stderr.String(), "-u root")
}

func TestOfferEOFAtEachCreateInputKeepsOriginalRefusal(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "create confirmation"},
		{name: "purpose", input: "y\n"},
		{name: "duration", input: "y\nrelease investigation\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := replaceStdin(t, tt.input)
			defer func() { _ = read.Close() }()
			var creates atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/iam/users/-/":
					_, _ = w.Write([]byte(`{"id":"user-1"}`))
				case r.URL.Path == "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
				case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
				case r.Method == http.MethodPost:
					creates.Add(1)
					http.Error(w, "unexpected create", http.StatusInternalServerError)
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			ios, _, _ := utils.TestIOStreams()
			selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: newTestClient(ts)}

			resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionExpired})

			assert.False(t, resend)
			require.NoError(t, failure)
			assert.Zero(t, creates.Load())
			assert.Equal(t, "old-session", selection.ID())
		})
	}
}

func TestPendingOfferBecomesActiveAndSavesWithoutAnotherStatusRead(t *testing.T) {
	setupTmpConfig(t)
	read := replaceStdin(t, "y\n")
	defer func() { _ = read.Close() }()
	now := time.Now().UTC()
	pending := activeServerSession("pending-session", now)
	pending.Status = wsapi.StatusPending
	pending.ApprovalRequestID = "approval-1"
	pending.ExpiresAt = time.Time{}
	active := activeServerSession("pending-session", now)
	var reads atomic.Int32
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/api/iam/users/-/":
			return http.StatusOK, `{"id":"user-1"}`
		case "/api/servers/servers/":
			return http.StatusOK, `{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`
		case "/api/work-sessions/sessions/":
			body, err := json.Marshal(map[string]any{"count": 1, "results": []wsapi.WorkSession{pending}})
			require.NoError(t, err)
			return http.StatusOK, string(body)
		case "/api/work-sessions/sessions/pending-session/":
			reads.Add(1)
			body, err := json.Marshal(active)
			require.NoError(t, err)
			return http.StatusOK, string(body)
		default:
			return http.StatusNotFound, `{}`
		}
	})}
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, canOffer: true, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: ac, interrupt: noInterrupt}
	var resentID string
	var failure error

	synctest.Test(t, func(*testing.T) {
		callbacks := selection.Offer(utils.ErrorHandlerCallbacks{RetryOperation: func() error {
			resentID = selection.ID()
			return nil
		}})
		failure = utils.HandleCommonErrors(utils.MarkSubmission(selectionCodedError{code: utils.WorkSessionRequired}), "web-1", callbacks)
	})

	require.NoError(t, failure)
	assert.Equal(t, int32(1), reads.Load())
	assert.Equal(t, "pending-session", resentID)
	assert.Equal(t, "pending-session", selection.ID())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Equal(t, "pending-session", stored)
}

func TestSettleSavesToClientPinnedWorkspaceAfterConfigSwitch(t *testing.T) {
	setupTmpConfig(t)
	switchConfigToWorkspace(t, "other", "other-session")
	now := time.Now().UTC()
	active := activeServerSession("new-session", now)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(active)
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, ios: ios, ac: newTestClient(ts)}
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}

	resend, failure := selection.settle(context.Background(), &active, false, want, "")

	assert.True(t, resend)
	require.NoError(t, failure)
	storedPinned, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Equal(t, "new-session", storedPinned)
	storedCurrent, err := config.GetActiveWorkSessionFor("other")
	require.NoError(t, err)
	assert.Equal(t, "other-session", storedCurrent)
}

func TestSafeResumeIDAcceptsOnlyPlainIdentifiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "uuid", id: "550e8400-e29b-41d4-a716-446655440000", want: true},
		{name: "underscore", id: "session_1", want: true},
		{name: "empty"},
		{name: "space", id: "session 1"},
		{name: "shell substitution", id: "session$(id)"},
		{name: "backtick", id: "session`id`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, safeResumeID(tt.id))
		})
	}
}

func TestOfferAllowed(t *testing.T) {
	t.Parallel()
	allowed := offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true}
	tests := []struct {
		name string
		env  offerEnv
		want bool
	}{
		{name: "interactive browser login", env: allowed, want: true},
		{name: "stdin redirected", env: offerEnv{stderrTTY: true, browserLogin: true}},
		{name: "stderr redirected", env: offerEnv{stdinTTY: true, browserLogin: true}},
		{name: "agent", env: offerEnv{stdinTTY: true, stderrTTY: true, agent: true, browserLogin: true}},
		{name: "json", env: offerEnv{stdinTTY: true, stderrTTY: true, json: true, browserLogin: true}},
		{name: "token", env: offerEnv{stdinTTY: true, stderrTTY: true}},
		{name: "opt out", env: offerEnv{stdinTTY: true, stderrTTY: true, browserLogin: true, optOut: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, offerAllowed(tt.env))
		})
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "required", code: utils.WorkSessionRequired, want: true},
		{name: "not usable", code: utils.WorkSessionNotUsable, want: true},
		{name: "scope", code: utils.WorkSessionScopeNotAllowed, want: true},
		{name: "server", code: utils.WorkSessionServerNotAllowed, want: true},
		{name: "expired", code: utils.WorkSessionExpired, want: true},
		{name: "not active", code: utils.WorkSessionNotActive},
		{name: "assignee mismatch", code: utils.WorkSessionAssigneeMismatch},
		{name: "account not approved", code: utils.WorkSessionAccountNotApproved},
		{name: "agent session required", code: utils.WorkSessionAgentSessionRequired},
		{name: "cluster not allowed", code: utils.WorkSessionClusterNotAllowed},
		{name: "unknown", code: "work_session_future"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, decide(tt.code))
		})
	}
}

func TestSelectionOffersOnceWhenLookupFails(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{canOffer: true, source: SourceNone, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: &client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client()}}
	refusal := selectionCodedError{code: utils.WorkSessionRequired}

	resend, failure := selection.handleRefusal("web-1", refusal)
	secondResend, secondFailure := selection.handleRefusal("web-1", refusal)

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.False(t, secondResend)
	require.NoError(t, secondFailure)
	assert.Equal(t, int32(1), requests.Load())
}

func TestFileCommandOfferCreatesAndRebindsOneSession(t *testing.T) {
	setupTmpConfig(t)
	read := replaceStdin(t, "y\nrelease investigation\n\n")
	defer func() { _ = read.Close() }()

	now := time.Now().UTC()
	var createdRequest wsapi.WorkSessionCreateRequest
	var createCount atomic.Int32
	submittedIDs := make(chan string, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/events/commands/":
			var body struct {
				WorkSession string `json:"work_session"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode command: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			submittedIDs <- body.WorkSession
			if body.WorkSession == "old-session" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code":"work_session_expired"}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"command-1"}]`))
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []wsapi.WorkSession{activeServerSession("old-session", now)}})
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodPost:
			createCount.Add(1)
			var request wsapi.WorkSessionCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			createdRequest = request
			_ = json.NewEncoder(w).Encode(activeServerSession("new-session", now))
		case r.URL.Path == "/api/work-sessions/sessions/old-session/":
			_ = json.NewEncoder(w).Encode(activeServerSession("old-session", now))
		case r.URL.Path == "/api/work-sessions/sessions/new-session/":
			_ = json.NewEncoder(w).Encode(activeServerSession("new-session", now))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{
		id: "old-session", canOffer: true, source: SourceConfig,
		need: Need{Operation: FileCommand, Server: "web-1", Account: "root"},
		ios:  ios, ac: newTestClient(ts),
	}

	submit := func() error {
		_, err := event.SubmitCommand(selection.ac, "web-1", "true", "root", "", nil, selection.ID(), "")
		return err
	}
	initial := submit()
	require.Error(t, initial)
	failure := utils.HandleCommonErrors(initial, "web-1", selection.Offer(utils.ErrorHandlerCallbacks{RetryOperation: submit}))

	require.NoError(t, failure)
	assert.Equal(t, wsapi.WorkSessionCreateRequest{
		Description: "release investigation", RequesterType: "user", Scopes: []string{"command", "webftp"},
		Servers: []string{"server-1"}, Duration: "01:00:00", TargetAccountUsername: "root",
	}, createdRequest)
	assert.Equal(t, int32(1), createCount.Load())
	assert.Equal(t, "new-session", selection.ID())
	assert.Equal(t, "old-session", <-submittedIDs)
	assert.Equal(t, "new-session", <-submittedIDs)
	assert.Empty(t, submittedIDs)
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Equal(t, "new-session", stored)
}

func activeServerSession(id string, now time.Time) wsapi.WorkSession {
	return wsapi.WorkSession{
		ID: id, Status: wsapi.StatusActive, RequesterType: "user",
		AssignedUser: &types.UserSummary{ID: "user-1"},
		Servers:      []types.ServerSummary{{ID: "server-1", Name: "web-1"}},
		Scopes:       []string{"command", "webftp"}, ExpiresAt: now.Add(time.Hour),
	}
}

func TestOfferDeclineDoesNotCreate(t *testing.T) {
	setupTmpConfig(t)
	read := replaceStdin(t, "n\nn\nshould not be read\n1h\n")
	defer func() { _ = read.Close() }()
	now := time.Now().UTC()
	var creates atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			response := struct {
				Count   int                 `json:"count"`
				Results []wsapi.WorkSession `json:"results"`
			}{Count: 1, Results: []wsapi.WorkSession{activeServerSession("other-session", now)}}
			_ = json.NewEncoder(w).Encode(response)
		case r.URL.Path == "/api/work-sessions/sessions/other-session/" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(activeServerSession("other-session", now))
		case r.Method == http.MethodPost:
			creates.Add(1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: newTestClient(ts)}

	resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionExpired})

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, int32(0), creates.Load())
	assert.Equal(t, "old-session", selection.ID())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestOfferCreateFailureKeepsOriginalRefusalPath(t *testing.T) {
	read := replaceStdin(t, "y\nrelease investigation\n\n")
	defer func() { _ = read.Close() }()
	var creates atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case r.Method == http.MethodPost:
			creates.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"work_session_expires_exceeds_max_ttl"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: newTestClient(ts)}

	resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionExpired})

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, int32(1), creates.Load())
	assert.Equal(t, "old-session", selection.ID())
	assert.Contains(t, stderr.String(), "Could not create a WorkSession")
	assert.Contains(t, stderr.String(), "ask for a shorter one")
}

func TestOfferCreateWithUnusableIDFailsInsteadOfReportingTheGate(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{name: "not a path segment", id: "../other"},
		{name: "unsafe in a resume command", id: "session$(id)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := replaceStdin(t, "y\nrelease investigation\n\n")
			defer func() { _ = read.Close() }()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/iam/users/-/":
					_, _ = w.Write([]byte(`{"id":"user-1"}`))
				case r.URL.Path == "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
				case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
				case r.Method == http.MethodPost:
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"id": tc.id, "status": wsapi.StatusActive})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			ios, _, _ := utils.TestIOStreams()
			selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: newTestClient(ts)}

			resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionRequired})

			assert.False(t, resend)
			var exitErr *utils.ExitError
			require.ErrorAs(t, failure, &exitErr)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
			assert.Contains(t, failure.Error(), "check for it before creating another")
			assert.NotContains(t, failure.Error(), tc.id)
			assert.Equal(t, "old-session", selection.ID())
		})
	}
}

func TestSettleRejectsADifferentSessionThanTheOneChosen(t *testing.T) {
	setupTmpConfig(t)
	now := time.Now().UTC()
	chosen, other := activeServerSession("chosen-session", now), activeServerSession("other-session", now)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(other)
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, ios: ios, ac: newTestClient(ts)}
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}

	resend, failure := selection.settle(context.Background(), &chosen, false, want, "")

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, "old-session", selection.ID())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.NotEqual(t, "other-session", stored)
}

func TestOfferWaitNamesTheOperationsResumeStep(t *testing.T) {
	t.Parallel()
	pending := wsapi.WorkSession{ID: "pending-session", Status: wsapi.StatusPending, RequesterType: "user"}
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		body, _ := json.Marshal(pending)
		return http.StatusOK, string(body)
	})}
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{ios: ios, ac: ac}
	selection.ResumeWith("upload the edited copy")

	synctest.Test(t, func(*testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = selection.settle(ctx, &pending, true, Want{}, "")
	})

	assert.Equal(t, 2, strings.Count(stderr.String(), "then upload the edited copy"), "notice and exit guidance: %q", stderr.String())
	assert.NotContains(t, stderr.String(), "re-run")
}

func TestOfferRejectedWaitReturnsNotApproved(t *testing.T) {
	read := replaceStdin(t, "y\nrelease investigation\n\n")
	defer func() { _ = read.Close() }()
	rejected := wsapi.WorkSession{ID: "new-session", Status: wsapi.StatusRejected, RequesterType: "user"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(rejected)
		case r.URL.Path == "/api/work-sessions/sessions/new-session/":
			_ = json.NewEncoder(w).Encode(rejected)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ios, _, _ := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: newTestClient(ts)}

	resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionExpired})

	assert.False(t, resend)
	var exit *utils.ExitError
	require.ErrorAs(t, failure, &exit)
	assert.Equal(t, utils.ExitCodeNotApproved, exit.Code)
	assert.Equal(t, "old-session", selection.ID())
}

func replaceStdin(t *testing.T, input string) *os.File {
	t.Helper()
	old := os.Stdin
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = read
	t.Cleanup(func() { os.Stdin = old })
	_, err = write.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, write.Close())
	return read
}

func TestCtrlCAtTheOfferPromptDeclinesWithoutCreating(t *testing.T) {
	setupTmpConfig(t)
	read, write, err := os.Pipe() // stdin stays open, so only Ctrl+C can end the prompt
	require.NoError(t, err)
	originalStdin := os.Stdin
	os.Stdin = read
	var creates atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			return http.StatusOK, `{"id":"user-1"}`
		case r.URL.Path == "/api/servers/servers/":
			return http.StatusOK, `{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`
		case r.Method == http.MethodPost:
			creates.Add(1)
			return http.StatusInternalServerError, `{}`
		default:
			time.AfterFunc(50*time.Millisecond, cancel) // Ctrl+C arrives while the prompt waits
			return http.StatusOK, `{"count":0,"results":[]}`
		}
	})}
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Transfer, Server: "web-1"}, ios: ios, ac: ac, interrupt: func() (context.Context, context.CancelFunc) {
		assert.Empty(t, stderr.String(), "Ctrl+C must be handled before anything is asked")
		return ctx, cancel
	}}
	t.Cleanup(func() { _ = write.Close(); selection.reads.Wait(); os.Stdin = originalStdin })

	resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionRequired})

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Zero(t, creates.Load())
	assert.Equal(t, "old-session", selection.ID())
	assert.Contains(t, stderr.String(), "Stopped; no WorkSession was selected.")
}

func TestOfferAccountGuidanceNamesTheOperationsCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation Operation
		want      string
	}{
		{name: "command", operation: Command, want: "run exec with -u root"},
		{name: "websh", operation: Websh, want: "run websh with -u root"},
		{name: "transfer", operation: Transfer, want: "run cp with -u root"},
		{name: "tunnel", operation: Tunnel, want: "run tunnel with -u root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pending := wsapi.WorkSession{ID: "pending-session", Status: wsapi.StatusPending, RequesterType: "user"}
			ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
				body, _ := json.Marshal(pending)
				return http.StatusOK, string(body)
			})}
			ios, _, stderr := utils.TestIOStreams()
			selection := &Selection{need: Need{Operation: tt.operation}, ios: ios, ac: ac}

			synctest.Test(t, func(*testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_, _ = selection.settle(ctx, &pending, true, Want{}, "root")
			})

			assert.Contains(t, stderr.String(), tt.want)
			if tt.operation != Command {
				assert.NotContains(t, stderr.String(), "run exec with")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCtrlCDuringAnOfferRequestEndsItWithoutSavingOrResending(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		stall  bool
		input  string
		want   string
	}{
		{name: "current user stalls", method: http.MethodGet, path: "/api/iam/users/-/", stall: true, want: "Stopped; no WorkSession was selected."},
		{name: "server lookup stalls", method: http.MethodGet, path: "/api/servers/servers/", stall: true, want: "Stopped; no WorkSession was selected."},
		{name: "session list stalls", method: http.MethodGet, path: "/api/work-sessions/sessions/", stall: true, want: "Stopped; no WorkSession was selected."},
		{name: "session list answers", method: http.MethodGet, path: "/api/work-sessions/sessions/", want: "Stopped; no WorkSession was selected."},
		{name: "verification stalls", method: http.MethodGet, path: "/api/work-sessions/sessions/reuse-session/", stall: true, input: "y\n", want: "Stopped before WorkSession reuse-session was selected."},
		{name: "verification answers", method: http.MethodGet, path: "/api/work-sessions/sessions/reuse-session/", input: "y\n", want: "Stopped before WorkSession reuse-session was selected."},
		{name: "create stalls", method: http.MethodPost, path: "/api/work-sessions/sessions/", stall: true, input: "n\ny\nfix\n1h\n", want: "it may have been created"},
		{name: "create answers", method: http.MethodPost, path: "/api/work-sessions/sessions/", input: "n\ny\nfix\n1h\n", want: "Stopped before WorkSession reuse-session was selected."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTmpConfig(t)
			read := replaceStdin(t, tt.input)
			defer func() { _ = read.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session, err := json.Marshal(activeServerSession("reuse-session", time.Now().UTC()))
			require.NoError(t, err)
			answers := map[string]string{
				"GET /api/iam/users/-/":                          `{"id":"user-1"}`,
				"GET /api/servers/servers/":                      `{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`,
				"GET /api/work-sessions/sessions/":               `{"count":1,"results":[` + string(session) + `]}`,
				"GET /api/work-sessions/sessions/reuse-session/": string(session),
				"POST /api/work-sessions/sessions/":              string(session),
			}
			ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == tt.method && r.URL.Path == tt.path {
					cancel() // Ctrl+C lands while this request is in flight
					if tt.stall {
						select {
						case <-r.Context().Done():
							return nil, r.Context().Err()
						case <-time.After(5 * time.Second):
							t.Errorf("%s %s ignored Ctrl+C", r.Method, r.URL.Path)
						}
					}
				}
				recorder := httptest.NewRecorder()
				recorder.Header().Set("Content-Type", "application/json")
				_, _ = recorder.WriteString(answers[r.Method+" "+r.URL.Path])
				return recorder.Result(), nil
			})}}
			ios, _, stderr := utils.TestIOStreams()
			selection := &Selection{id: "old-session", source: SourceConfig, need: Need{Operation: Command, Server: "web-1"}, ios: ios, ac: ac, interrupt: func() (context.Context, context.CancelFunc) { return ctx, cancel }}

			resend, failure := selection.handleRefusal("web-1", selectionCodedError{code: utils.WorkSessionRequired})

			assert.False(t, resend)
			require.NoError(t, failure)
			assert.Equal(t, "old-session", selection.ID())
			assert.Contains(t, stderr.String(), tt.want)
			stored, err := config.GetActiveWorkSessionFor("ws")
			require.NoError(t, err)
			assert.Empty(t, stored)
		})
	}
}

// answeredContext has ended by the time its Done is read, and by then the prompt has answered too.
type answeredContext struct {
	context.Context
	answered func()
}

func (c answeredContext) Done() <-chan struct{} { c.answered(); return c.Context.Done() }

func TestCtrlCWinsOverAnAnswerThatArrivesWithIt(t *testing.T) {
	t.Parallel()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for range 50 { // select picks at random between the two
		ios, _, _ := utils.TestIOStreams()
		selection := &Selection{ios: ios}

		_, err := selection.read(answeredContext{Context: ended, answered: selection.reads.Wait}, func() (string, error) { return "y", nil })

		require.ErrorIs(t, err, errPromptInterrupted)
	}
}

func TestCtrlCDuringTheConfigSaveDoesNotResend(t *testing.T) {
	setupTmpConfig(t)
	active := activeServerSession("new-session", time.Now().UTC())
	body, err := json.Marshal(active)
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	held, err := filelock.Acquire(filepath.Join(home, ".alpacon", "config.json.lock"), 0644)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, WorkspaceName: "ws", HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		time.AfterFunc(300*time.Millisecond, func() { cancel(); _ = held.Release() }) // the save is waiting by now
		return http.StatusOK, string(body)
	})}
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, ios: ios, ac: ac}
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}

	resend, failure := selection.settle(ctx, &active, false, want, "")

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, "old-session", selection.ID())
	assert.Contains(t, stderr.String(), "the request was not sent again")
}

func TestSettleDoesNotSaveIntoALoginMadeWhileItWaited(t *testing.T) {
	setupTmpConfig(t)
	active := activeServerSession("new-session", time.Now().UTC())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "token-b", "refresh-b", "", 3600, false)) // another terminal
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(active)
	}))
	defer ts.Close()
	ac := loggedInClient(t, ts)
	ios, _, stderr := utils.TestIOStreams()
	selection := &Selection{id: "old-session", source: SourceConfig, ios: ios, ac: ac}
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}

	resend, failure := selection.settle(context.Background(), &active, false, want, "")

	assert.False(t, resend)
	require.NoError(t, failure)
	assert.Equal(t, "old-session", selection.ID())
	assert.Contains(t, stderr.String(), config.ErrLoginChanged.Error())
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Empty(t, stored)
}
