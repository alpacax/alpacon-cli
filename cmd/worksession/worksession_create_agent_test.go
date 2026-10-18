package worksession

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentHelperEnv clears every variable agent detection reads, then sets the
// given "KEY=value" entries, for a helper process whose result must not depend
// on the shell the tests run in.
func agentHelperEnv(set ...string) []string {
	var env []string
	for _, name := range aiagent.Variables() {
		env = append(env, name+"=")
	}
	return append(env, set...)
}

// createRecorder answers a work-session create and keeps what reached it.
type createRecorder struct {
	mu       sync.Mutex
	requests int
	body     map[string]any
	header   http.Header
}

func newCreateRecorderServer(t *testing.T) (*httptest.Server, *createRecorder) {
	t.Helper()
	rec := &createRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.requests++
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decode body %q: %v", raw, err)
			}
			rec.mu.Lock()
			rec.body = body
			rec.header = r.Header.Clone()
			rec.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"ses-x","status":"active","expires_at":"2026-06-01T12:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, rec
}

func (rec *createRecorder) snapshot() (requests int, body map[string]any, header http.Header) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.requests, rec.body, rec.header
}

func createArgs(extra ...string) []string {
	return append([]string{
		"create", "--purpose", "incident", "--scope", "command", "--server", "prod",
		"--expires-at", "2026-06-01T12:00:00Z",
	}, extra...)
}

func TestWorkSessionCreate_RequesterTypeUserRefusedUnderDetectedAgent(t *testing.T) {
	t.Parallel()
	env := agentHelperEnv(
		"AI_AGENT=claude-code",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"CODEX_THREAD_ID=thread-1",
	)
	for _, outputFormat := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(outputFormat, func(t *testing.T) {
			t.Parallel()
			ts, rec := newCreateRecorderServer(t)

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, env, outputFormat, ts.URL,
				createArgs("--requester-type", "user")...)

			assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "--requester-type user is refused")
			assert.Contains(t, stderr, "claude-code (from AI_AGENT, CLAUDE_CODE_CHILD_SESSION, CODEX_THREAD_ID)",
				"the refusal names the agent and every marker that fired")
			requests, _, _ := rec.snapshot()
			assert.Zero(t, requests, "the refusal comes before any request")

			if outputFormat == utils.OutputFormatJSON {
				var envelope errorEnvelope
				require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
				assert.Equal(t, utils.ExitCodeUsageError, envelope.ExitCode)
				assert.Equal(t, "usage_error", envelope.ErrorCode)
				assert.Equal(t, opCreate, envelope.Context.Operation)
			}
		})
	}
}

func TestWorkSessionCreate_DetectedAgentGetsAgentSession(t *testing.T) {
	t.Parallel()
	ts, rec := newCreateRecorderServer(t)

	_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv("CODEX_THREAD_ID=thread-1"),
		utils.OutputFormatTable, ts.URL, createArgs()...)

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	_, body, header := rec.snapshot()
	require.NotNil(t, body, "the create request did not reach the server")
	assert.Equal(t, "agent", body["requester_type"])
	assert.Equal(t, []string{"codex"}, header.Values(aiagent.NameHeader))
}

func TestWorkSessionCreate_NoAgentKeepsUserSession(t *testing.T) {
	t.Parallel()
	ts, rec := newCreateRecorderServer(t)

	_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(),
		utils.OutputFormatTable, ts.URL, createArgs()...)

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	_, body, header := rec.snapshot()
	require.NotNil(t, body, "the create request did not reach the server")
	assert.Equal(t, "user", body["requester_type"])
	assert.NotContains(t, header, aiagent.NameHeader)
}

func TestWorkSessionCreate_SudoBypassMatchesAgentDeclaration(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		env    []string
		extra  []string
		bypass bool
	}{
		{name: "person", env: agentHelperEnv()},
		{name: "requester type alone is no agent declaration", env: agentHelperEnv(), extra: []string{"--requester-type", "agent"}},
		{name: "declared agent", env: agentHelperEnv("AI_AGENT=codex"), bypass: true},
		{name: "detected agent", env: agentHelperEnv("CODEX_THREAD_ID=thread-1"), bypass: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts, rec := newCreateRecorderServer(t)
			extra := append([]string{"--sudo", "systemctl restart nginx"}, tt.extra...)
			_, stderr, exitCode := runWorkSessionHelperWithEnv(t, tt.env,
				utils.OutputFormatJSON, ts.URL, createArgs(extra...)...)
			require.Zero(t, exitCode, "stderr: %s", stderr)
			_, body, header := rec.snapshot()
			require.NotNil(t, body)
			assert.Equal(t, []any{"command", "sudo"}, body["scopes"])
			assert.Equal(t, []any{map[string]any{
				"commands": []any{"systemctl restart nginx"}, "allow_bypass_mfa": tt.bypass,
			}}, body["sudo_policies"])
			assert.Equal(t, tt.bypass, header.Get(aiagent.NameHeader) != "")
		})
	}
}

func TestWorkSessionCreate_AccountReachesRequestBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		extra []string
		want  any // nil: the field is left out
	}{
		{name: "named", extra: []string{"--account", "root"}, want: "root"},
		{name: "trimmed", extra: []string{"--account", "  deploy  "}, want: "deploy"},
		{name: "not passed", extra: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts, rec := newCreateRecorderServer(t)

			_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(),
				utils.OutputFormatTable, ts.URL, createArgs(tt.extra...)...)

			require.Equal(t, 0, exitCode, "stderr: %s", stderr)
			_, body, _ := rec.snapshot()
			require.NotNil(t, body, "the create request did not reach the server")
			if tt.want == nil {
				assert.NotContains(t, body, "target_account_username")
				return
			}
			assert.Equal(t, tt.want, body["target_account_username"])
		})
	}
}

func TestWorkSessionCreate_BlankAccountRefused(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "   "} {
		t.Run("value="+value, func(t *testing.T) {
			t.Parallel()
			ts, rec := newCreateRecorderServer(t)

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(),
				utils.OutputFormatTable, ts.URL, createArgs("--account", value)...)

			assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "--account must name an account, such as root")
			requests, _, _ := rec.snapshot()
			assert.Zero(t, requests, "the refusal comes before any request")
		})
	}
}

// newPendingCreateServer answers a work-session create with a session that
// stays pending, on the create and on every poll.
func newPendingCreateServer(t *testing.T) *httptest.Server {
	t.Helper()
	const pending = `{"id":"ses-x","status":"pending","approval_request_id":"apr-1","expires_at":"2026-06-01T12:00:00Z"}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(pending))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-x/":
			_, _ = w.Write([]byte(pending))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// An agent session cannot be attached with 'work-session use', so the
// follow-up for a pending agent session names --work-session <ID> instead,
// whether the agent was detected or declared and whether or not it waited.
func TestWorkSessionCreate_PendingFollowUpMatchesRequesterType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     []string
		extra   []string
		want    string
		notWant string
	}{
		{name: "detected agent", env: agentHelperEnv("CODEX_THREAD_ID=thread-1"),
			want: "--work-session ses-x", notWant: "work-session use"},
		{name: "detected agent, wait expired", env: agentHelperEnv("CODEX_THREAD_ID=thread-1"), extra: []string{"--wait-approval", "1ms"},
			want: "--work-session ses-x", notWant: "work-session use"},
		{name: "declared agent", env: agentHelperEnv(), extra: []string{"--requester-type", "agent"},
			want: "--work-session ses-x", notWant: "work-session use"},
		{name: "user", env: agentHelperEnv(),
			want: "alpacon work-session use ses-x", notWant: "--work-session ses-x"},
		{name: "user, wait expired", env: agentHelperEnv(), extra: []string{"--wait-approval", "1ms"},
			want: "alpacon work-session use ses-x", notWant: "--work-session ses-x"},
	}
	for _, tt := range tests {
		for _, outputFormat := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(tt.name+"/"+outputFormat, func(t *testing.T) {
				t.Parallel()
				ts := newPendingCreateServer(t)

				stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, tt.env, outputFormat, ts.URL,
					createArgs(tt.extra...)...)

				require.Equal(t, utils.ExitCodePendingApproval, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
				guidance := stderr
				if outputFormat == utils.OutputFormatJSON {
					var envelope struct {
						Status      string             `json:"status"`
						NextActions []utils.NextAction `json:"next_actions"`
					}
					require.NoError(t, json.Unmarshal([]byte(stdout), &envelope), "stdout: %s", stdout)
					assert.Equal(t, utils.PendingApprovalStatus, envelope.Status)
					require.NotEmpty(t, envelope.NextActions)
					raw, err := json.Marshal(envelope.NextActions)
					require.NoError(t, err)
					guidance = string(raw)
				}
				assert.Contains(t, guidance, tt.want)
				assert.NotContains(t, guidance, tt.notWant)
			})
		}
	}
}

// Every scope the work-session gate recommends to an agent must pass the same
// check 'work-session create' runs, or following the recovery fails locally.
func TestAgentSessionScope_PassesValidateAgentScopes(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{utils.OperationCommand, utils.OperationWebFTP, utils.OperationWebsh} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			scope := utils.AgentSessionScope(operation)
			require.NoError(t, validateAgentScopes("agent", []string{scope}))
			if utils.IsAgentForbiddenScope(operation) {
				assert.Error(t, validateAgentScopes("agent", []string{operation}))
			} else {
				assert.Equal(t, operation, scope, "an allowed scope is recommended as is")
			}
		})
	}
}

func TestAfterApprovalAction_NamesAccount(t *testing.T) {
	t.Parallel()
	agent := afterApprovalAction("agent", "s-1", "root")
	assert.Contains(t, agent.Description, "pass --work-session s-1")
	assert.Contains(t, agent.Description, "exec with -u root")
	user := afterApprovalAction("user", "s-1", "root")
	assert.Equal(t, "alpacon work-session use s-1", user.Command)
	assert.Contains(t, user.Description, "exec with -u root")
	assert.NotContains(t, afterApprovalAction("agent", "s-1", "").Description, "-u ")
	assert.Equal(t, "after approval", afterApprovalAction("user", "s-1", "").Description)
}
