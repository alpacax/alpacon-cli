package mfa

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckMFACompletion_Completed(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, mfaCompletionURL, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"completed": true}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	completed, err := CheckMFACompletion(ac)
	require.NoError(t, err)
	assert.True(t, completed)
}

func TestCheckMFACompletion_NotCompleted(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"completed": false}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	completed, err := CheckMFACompletion(ac)
	require.NoError(t, err)
	assert.False(t, completed)
}

func TestCheckMFACompletion_ServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	_, err := CheckMFACompletion(ac)
	assert.Error(t, err)
}

func mfaLinkServer(t *testing.T, body string) (*client.AlpaconClient, *url.Values) {
	t.Helper()
	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, mfaURL+"/", r.URL.Path)
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, WorkspaceName: "my-workspace"}, &query
}

func TestGetMFALink_ReturnsTheURLAndScopesItToTheServer(t *testing.T) {
	t.Parallel()
	ac, query := mfaLinkServer(t, `{"mfa_url": "https://example.com/mfa"}`)

	link, err := GetMFALink(ac, "server-id")

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/mfa", link)
	assert.Equal(t, "cli", query.Get("location"))
	assert.Equal(t, "server-id", query.Get("server"))
	assert.Equal(t, "my-workspace", query.Get("workspace"))
}

// A malformed body must not yield ("", nil): callers would open a browser on a blank link.
func TestGetMFALink_MalformedBodyIsAnError(t *testing.T) {
	t.Parallel()
	ac, _ := mfaLinkServer(t, `not json`)

	link, err := GetMFALink(ac, "server-id")

	require.Error(t, err)
	assert.Empty(t, link)
}

func TestGetMFALink_EmptyURLIsAnError(t *testing.T) {
	t.Parallel()
	ac, _ := mfaLinkServer(t, `{"mfa_url": ""}`)

	link, err := GetMFALink(ac, "server-id")

	require.Error(t, err)
	assert.Empty(t, link)
}

// The server answers a link request with no link once the client's MFA is already fresh. It
// stays an error for a caller that needs the link, and is told apart by the sentinel.
func TestGetWorkspaceSecurityMFALink_AlreadyDoneIsTheSentinel(t *testing.T) {
	t.Parallel()
	ac, _ := mfaLinkServer(t, `{"detail": "MFA is already done."}`)

	link, err := GetWorkspaceSecurityMFALink(ac)

	require.ErrorIs(t, err, ErrMFAAlreadyDone)
	assert.Empty(t, link)
	assert.Contains(t, err.Error(), "MFA URL is empty in server response")
}

func TestGetWorkspaceSecurityMFALink_SendsNoServerScope(t *testing.T) {
	t.Parallel()
	ac, query := mfaLinkServer(t, `{"mfa_url": "https://example.com/mfa"}`)

	link, err := GetWorkspaceSecurityMFALink(ac)

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/mfa", link)
	assert.Equal(t, "cli", query.Get("location"))
	assert.Equal(t, "my-workspace", query.Get("workspace"))
	assert.NotContains(t, *query, "server")
}

func TestGetMFALink_ServerErrorIsAnError(t *testing.T) {
	t.Parallel()
	reached := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient:    ts.Client(),
		BaseURL:       ts.URL,
		WorkspaceName: "my-workspace",
	}

	link, err := GetMFALink(ac, "server-id")

	require.Error(t, err)
	assert.Empty(t, link)
	assert.True(t, reached, "the error must come from the server, not from a guard before the request")
}

func TestErrorCallbacks_WiresEveryField(t *testing.T) {
	t.Parallel()
	ac := &client.AlpaconClient{}
	retried := false

	cb := ErrorCallbacks(ac, func() error { retried = true; return nil })

	assert.NotNil(t, cb.OnMFARequired)
	assert.NotNil(t, cb.OnUsernameRequired)

	require.NotNil(t, cb.RetryOperation)
	require.NoError(t, cb.RetryOperation())
	assert.True(t, retried, "RetryOperation must be the closure passed in")
}

func TestGetMFALink_EmptyWorkspaceIsAnErrorBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	link, err := GetMFALink(ac, "server-id")

	require.Error(t, err)
	assert.Empty(t, link)
	assert.False(t, called, "an empty workspace must not reach the server")
}

func TestGetMFALinkByServerName_NamesTheWorkspaceTheClientIsPinnedTo(t *testing.T) {
	// Regression guard: an empty HOME leaves no config, so a second config read fails here.
	t.Setenv("HOME", t.TempDir())

	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == mfaURL+"/" {
			query = r.URL.Query()
			_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
			return
		}
		_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "server-id"}]}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient:    ts.Client(),
		BaseURL:       ts.URL,
		WorkspaceName: "pinned-ws",
	}

	link, err := GetMFALinkByServerName(ac, "my-server")

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/mfa", link)
	assert.Equal(t, "server-id", query.Get("server"))
	assert.Equal(t, "pinned-ws", query.Get("workspace"))
}

func TestGetMFALinkByServerName_EmptyWorkspaceCostsNoRoundTrip(t *testing.T) {
	t.Parallel()
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	link, err := GetMFALinkByServerName(ac, "my-server")

	require.Error(t, err)
	assert.Empty(t, link)
	assert.False(t, called, "the name lookup must not run when no workspace can be named")
}

func TestStepUpForSudo_PollsAtAFixedInterval(t *testing.T) {
	const tick = 10 * time.Millisecond
	// Long enough that a widening schedule would have reached its widest gap
	// twice over, and off the tick grid: a deadline landing on the same instant as
	// a poll leaves the select to pick between them.
	const waitFor = 125*tick + tick/2

	var polls testutil.PollRecorder
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "pinned-ws",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case mfaURL + "/":
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case mfaCompletionURL:
				polls.Record()
				return http.StatusOK, `{"completed": false}`
			default:
				return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
			}
		}),
	}

	var err error
	synctest.Test(t, func(*testing.T) {
		err = stepUpForSudo(ac, "my-server", tick, waitFor)
	})

	require.Error(t, err)
	// A person is watching a browser through this window, so the gap stays put:
	// widening it would push the worst-case detection lag from one tick to ten.
	assert.Equal(t, tick, polls.WidestGap(),
		"no poll may sit out longer than the base tick")
}

// Completion ends the wait on the poll that sees it, and the token refresh after it
// is what the retry sees. HOME is an empty dir so the refresh has no stored token:
// the failure a user reads when a step-up completes but the session cannot renew.
func TestStepUpForSudo_CompletionEndsTheWaitAndRefreshesTheToken(t *testing.T) {
	const tick = 10 * time.Millisecond
	const waitFor = 125*tick + tick/2

	t.Setenv("HOME", t.TempDir())

	var polls testutil.PollRecorder
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "pinned-ws",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case mfaURL + "/":
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case mfaCompletionURL:
				polls.Record()
				return http.StatusOK, `{"completed": true}`
			default:
				return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
			}
		}),
	}

	var err error
	synctest.Test(t, func(*testing.T) {
		err = stepUpForSudo(ac, "my-server", tick, waitFor)
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh token after MFA")
	assert.NotContains(t, err.Error(), "timed out", "completion must end the wait, not run it to the deadline")
	assert.Equal(t, time.Duration(0), polls.WidestGap(),
		"the first completed read must end the wait rather than schedule another poll")
}

// A step-up whose completion checks are refused times out with the last
// refusal behind it, so its hint still prints; the timeout message is unchanged.
func TestStepUpForSudo_TimeoutOnARefusalKeepsTheHint(t *testing.T) {
	const tick = 10 * time.Millisecond
	const hint = "This credential cannot prove an MFA check; verify the sudo request from a console or CLI sign-in instead."
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "pinned-ws",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case mfaURL + "/":
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case mfaCompletionURL:
				return http.StatusForbidden, `{"code": "sudo_verify_credential_cannot_prove_mfa", "hint": "` + hint + `"}`
			default:
				return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
			}
		}),
	}

	var err error
	synctest.Test(t, func(*testing.T) {
		err = stepUpForSudo(ac, "my-server", tick, 10*tick+tick/2)
	})

	require.Error(t, err)
	assert.Equal(t, "MFA step-up timed out after 105ms", err.Error())
	assert.Equal(t, hint, utils.RefusalHint(err))
	// Only the hint goes with the timeout: the refusal's code would read as that
	// refusal to an MFA retry.
	assert.Empty(t, utils.StructuredErrorCode(err))
	assert.Zero(t, utils.HTTPStatusCode(err))
}

// completionClient answers the completion endpoint from replies in turn, repeating the last one,
// and records when each poll arrived.
func completionClient(polls *testutil.PollRecorder, replies ...func(w http.ResponseWriter)) *client.AlpaconClient {
	var served int
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "pinned-ws",
		HTTPClient: testutil.HandlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != mfaCompletionURL {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			polls.Record()
			reply := replies[min(served, len(replies)-1)]
			served++
			w.Header().Set("Content-Type", "application/json")
			reply(w)
		})),
	}
}

func completedReply(done bool) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if done {
			_, _ = w.Write([]byte(`{"completed": true}`))
			return
		}
		_, _ = w.Write([]byte(`{"completed": false}`))
	}
}

func statusReply(status int, body string, header ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestAwaitMFACompletion_EndsOnThePollThatSeesItAtAFixedTick(t *testing.T) {
	t.Parallel()
	const tick = 10 * time.Millisecond
	var polls testutil.PollRecorder
	// Past ten ticks, where utils.NextPollTick would start widening the gap.
	replies := make([]func(http.ResponseWriter), 0, 15)
	for range 14 {
		replies = append(replies, completedReply(false))
	}
	ac := completionClient(&polls, append(replies, completedReply(true))...)

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := AwaitMFACompletion(ac, tick, time.Minute)

		require.NoError(t, err)
		assert.Equal(t, 15*tick, time.Since(start), "the fifteenth poll sees the MFA and ends the wait")
	})
	assert.Equal(t, tick, polls.WidestGap(), "a person is watching a browser, so the gap never widens")
}

func TestAwaitMFACompletion_TimeoutMatchesTheSentinel(t *testing.T) {
	t.Parallel()
	const tick = 10 * time.Millisecond
	var polls testutil.PollRecorder
	ac := completionClient(&polls, completedReply(false))

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := AwaitMFACompletion(ac, tick, 10*tick+tick/2)

		require.ErrorIs(t, err, ErrMFAWaitTimedOut)
		assert.Equal(t, "MFA verification timed out after 105ms", err.Error())
		assert.Equal(t, 10*tick, time.Since(start), "no poll may start past the deadline")
	})
}

// A throttled poll stretches the next gap to the server's Retry-After and does not end the wait,
// and a timeout after it keeps only the throttle's hint, never its code or status.
func TestAwaitMFACompletion_ThrottleStretchesTheGap(t *testing.T) {
	t.Parallel()
	// The backoff cap is 60 ticks, so this tick lets a one-second Retry-After through whole.
	const tick = 100 * time.Millisecond
	const hint = "Too many checks; wait a moment."
	var polls testutil.PollRecorder
	throttled := statusReply(http.StatusTooManyRequests, `{"code": "throttled", "hint": "`+hint+`"}`, "Retry-After", "1")
	ac := completionClient(&polls, throttled, completedReply(true))

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := AwaitMFACompletion(ac, tick, time.Minute)

		require.NoError(t, err)
		assert.Equal(t, tick+time.Second, time.Since(start))
	})

	synctest.Test(t, func(t *testing.T) {
		alwaysThrottled := completionClient(&testutil.PollRecorder{}, throttled)
		err := AwaitMFACompletion(alwaysThrottled, tick, 3*time.Second+tick/2)

		require.ErrorIs(t, err, ErrMFAWaitTimedOut)
		assert.Equal(t, hint, utils.RefusalHint(err))
		assert.Empty(t, utils.StructuredErrorCode(err))
		assert.Zero(t, utils.HTTPStatusCode(err))
	})
}

func TestAwaitMFACompletion_EndsOnARefusalAtOnce(t *testing.T) {
	t.Parallel()
	const tick = 10 * time.Millisecond
	var polls testutil.PollRecorder
	ac := completionClient(&polls, statusReply(http.StatusUnauthorized, `{"code": "auth_client_instance_revoked"}`))

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := AwaitMFACompletion(ac, tick, time.Minute)

		require.Error(t, err)
		require.NotErrorIs(t, err, ErrMFAWaitTimedOut)
		assert.Equal(t, "auth_client_instance_revoked", utils.StructuredErrorCode(err))
		assert.Equal(t, tick, time.Since(start), "polling again cannot change a refusal")
	})
}

func TestAwaitMFACompletion_RidesThroughFewerThanTheFailureLimit(t *testing.T) {
	t.Parallel()
	const tick = 10 * time.Millisecond
	unavailable := statusReply(http.StatusServiceUnavailable, `{}`)

	t.Run("recovers", func(t *testing.T) {
		t.Parallel()
		replies := make([]func(http.ResponseWriter), 0, utils.MaxConsecutivePollFailures)
		for range utils.MaxConsecutivePollFailures - 1 {
			replies = append(replies, unavailable)
		}
		ac := completionClient(&testutil.PollRecorder{}, append(replies, completedReply(true))...)
		synctest.Test(t, func(t *testing.T) {
			require.NoError(t, AwaitMFACompletion(ac, tick, time.Minute))
		})
	})

	t.Run("gives_up", func(t *testing.T) {
		t.Parallel()
		var polls testutil.PollRecorder
		ac := completionClient(&polls, unavailable)
		synctest.Test(t, func(t *testing.T) {
			err := AwaitMFACompletion(ac, tick, time.Minute)

			require.Error(t, err)
			require.NotErrorIs(t, err, ErrMFAWaitTimedOut)
			assert.Equal(t, http.StatusServiceUnavailable, utils.HTTPStatusCode(err))
		})
		assert.Equal(t, tick, polls.WidestGap(), "a failed poll keeps the base tick, as the person may finish MFA meanwhile")
	})
}
