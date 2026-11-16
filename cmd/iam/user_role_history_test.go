package iam

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const historySubjectID = "550e8400-e29b-41d4-a716-446655440000"

// A row an auditor reads on the bound rung whose binding names a server they do not reach.
const hiddenRow = `{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","record_class":"role_binding","action":"granted",
	"role":{"id":"r1","name":"server:viewer"},"role_name":"server:viewer","scope":"object",
	"principal":{"type":"iam.user","id":"550e8400-e29b-41d4-a716-446655440000","label":"carol"},
	"changed_by":{"id":"u2","label":"bob"},"reason":"","context":{"scope_content_type":"servers.server"},
	"targets_hidden":1,"added_at":"2026-11-10T09:00:00Z"}`

// historyServer answers the audit log with pages[N-1] for page N and fails on anything else.
func historyServer(t *testing.T, pages ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rbac/role-audit-logs/" {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if got := r.URL.Query().Get("user"); got != historySubjectID {
			t.Errorf("user filter = %q, want %q", got, historySubjectID)
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 || page > len(pages) {
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[page-1]))
	}))
}

// runHistory runs the command in process under format and returns its streams and error.
// Serial callers only: it sets utils.OutputFormat and swaps os.Stdout and os.Stderr.
func runHistory(t *testing.T, ts *httptest.Server, format string, args ...string) (string, string, error) {
	t.Helper()
	saved := utils.OutputFormat
	t.Cleanup(func() { utils.OutputFormat = saved })
	utils.OutputFormat = format

	cmd := newUserRoleHistoryCmd(func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
	})
	cmd.SetArgs(append([]string{historySubjectID}, args...))

	var err error
	stdout, stderr := testutil.CaptureOutput(t, func() { err = cmd.Execute() })
	return stdout, stderr, err
}

func historyPage(reach, rows string) string {
	if reach == "" {
		return `{"count":1,"current":1,"next":null,"last":1,"results":[` + rows + `]}`
	}
	return `{"count":1,"current":1,"next":null,"last":1,"reach":` + reach + `,"results":[` + rows + `]}`
}

// Serial: runHistory sets utils.OutputFormat and captures the process's stdout and stderr.
func TestUserRoleHistory_TableSaysHowFarTheListReaches(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStderr string
		hiddenRow  bool
	}{
		{name: "org", body: historyPage(`"org"`, hiddenRow), wantStderr: "Reach: org—you may read every role change in this workspace", hiddenRow: true},
		{name: "bound", body: historyPage(`"bound"`, hiddenRow), wantStderr: "Reach: bound—beyond the changes that name you", hiddenRow: true},
		{name: "self", body: historyPage(`"self"`, hiddenRow), wantStderr: "Reach: self—only changes that name you", hiddenRow: true},
		{name: "an unknown value is shown as sent", body: historyPage(`"tenant"`, hiddenRow), wantStderr: "Reach: tenant—this version of alpacon does not recognize it, so the list may not be complete.", hiddenRow: true},
		{name: "a server that states none", body: historyPage("", hiddenRow), wantStderr: "The server did not say how far this list reaches, so it may not be complete.", hiddenRow: true},
		// A direct Application maintainer is bound too, and through a per-user list sees only
		// their own rows: the line must still hold for an empty table.
		{name: "bound with no rows", body: historyPage(`"bound"`, ""), wantStderr: "Reach: bound—"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := historyServer(t, tt.body)
			defer ts.Close()

			stdout, stderr, err := runHistory(t, ts, utils.OutputFormatTable)

			require.NoError(t, err)
			assert.Contains(t, stderr, tt.wantStderr)
			assert.NotContains(t, stdout, "Reach", "stdout carries only the table")
			assert.Contains(t, stdout, "TARGET")
			if tt.hiddenRow {
				assert.Contains(t, stdout, "servers.server (1 hidden)")
				assert.Contains(t, stdout, "(withheld)")
			}
		})
	}
}

// Serial: runHistory sets utils.OutputFormat and captures the process's stdout and stderr.
func TestUserRoleHistory_JSONCarriesTheReach(t *testing.T) {
	hiddenEntry := `{"action":"granted","role":"server:viewer","scope":"object",
		"target_type":"servers.server","target_id":null,"targets_hidden":1,
		"changed_by":"bob","reason":"","erased":false,"at":"2026-11-10T09:00:00Z"}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "bound", body: historyPage(`"bound"`, hiddenRow), want: `{"reach":"bound","entries":[` + hiddenEntry + `]}`},
		{name: "a server that states none", body: historyPage("", hiddenRow), want: `{"reach":null,"entries":[` + hiddenEntry + `]}`},
		{name: "an empty list", body: historyPage(`"self"`, ""), want: `{"reach":"self","entries":[]}`},
		{name: "a non-string value is passed through", body: historyPage(`{"kind":"org"}`, ""), want: `{"reach":{"kind":"org"},"entries":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := historyServer(t, tt.body)
			defer ts.Close()

			stdout, stderr, err := runHistory(t, ts, utils.OutputFormatJSON)

			require.NoError(t, err)
			assert.JSONEq(t, tt.want, stdout)
			assert.Empty(t, stderr)
		})
	}
}

// Serial: runHistory sets utils.OutputFormat and captures the process's stdout and stderr.
func TestUserRoleHistory_ReachThatChangedBetweenPages(t *testing.T) {
	rows := func(from, n int) string {
		var buf bytes.Buffer
		for i := range n {
			if i > 0 {
				buf.WriteString(",")
			}
			buf.WriteString(`{"id":"` + strconv.Itoa(from+i) + `","action":"granted","role_name":"operator","scope":"global"}`)
		}
		return buf.String()
	}
	first := `{"count":150,"current":1,"next":2,"last":2,"reach":"bound","results":[` + rows(0, 100) + `]}`
	second := `{"count":150,"current":2,"next":null,"last":2,"reach":"self","results":[` + rows(100, 50) + `]}`

	ts := historyServer(t, first, second)
	defer ts.Close()

	stdout, stderr, err := runHistory(t, ts, utils.OutputFormatJSON, "--tail", "150")

	require.NoError(t, err)
	var output roleHistoryOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &output))
	assert.JSONEq(t, `"bound"`, string(output.Reach), "the first page's value is the one reported")
	assert.True(t, output.ReachChanged, "reach_changed must be set: %s", stdout)
	assert.Len(t, output.Entries, 150)
	assert.Contains(t, stderr, "The pages of the role history did not state the same reach")
}

// Serial: runHistory sets utils.OutputFormat and captures the process's stdout and stderr.
func TestUserRoleHistory_NoneExitsOne(t *testing.T) {
	for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(format, func(t *testing.T) {
			ts := historyServer(t, historyPage(`"none"`, ""))
			defer ts.Close()

			stdout, stderr, err := runHistory(t, ts, format)

			assert.Empty(t, stderr, "the command must exit before printing a reach line")
			exitErr, ok := errors.AsType[*utils.ExitError](err)
			require.True(t, ok, "want an ExitError, got %v", err)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
			assert.Empty(t, stdout, "an empty list must not be printed as if it were the answer")
			var printed bytes.Buffer
			utils.FprintExitError(&printed, exitErr)
			if format == utils.OutputFormatJSON {
				var envelope struct {
					ErrorCode string `json:"error_code"`
					Message   string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(printed.Bytes(), &envelope), printed.String())
				assert.Equal(t, errorCodeReachNone, envelope.ErrorCode)
				assert.Contains(t, envelope.Message, "may not read the role-change log (reach: none)")
				return
			}
			assert.Contains(t, printed.String(), "may not read the role-change log (reach: none)")
		})
	}
}

// Serial: runHistory sets utils.OutputFormat and captures the process's stdout and stderr.
func TestUserRoleHistory_ARefusalKeepsItsMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","detail":"denied"}`))
	}))
	defer ts.Close()

	_, _, err := runHistory(t, ts, utils.OutputFormatTable)

	exitErr, ok := errors.AsType[*utils.ExitError](err)
	require.True(t, ok, "want an ExitError, got %v", err)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	var printed bytes.Buffer
	utils.FprintExitError(&printed, exitErr)
	assert.Contains(t, printed.String(), "Failed to retrieve the role history")
	assert.Contains(t, printed.String(), "not an account you may read", "the refusal must be described, not dropped")
}

func TestReachLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		reach          rbac.Reach
		want           string
		wantIncomplete bool
	}{
		{name: "org", reach: rbac.Reach{Raw: json.RawMessage(`"org"`), Value: rbac.ReachOrg}, want: "Reach: org—"},
		{name: "bound", reach: rbac.Reach{Raw: json.RawMessage(`"bound"`), Value: rbac.ReachBound}, want: "Reach: bound—"},
		{name: "self", reach: rbac.Reach{Raw: json.RawMessage(`"self"`), Value: rbac.ReachSelf}, want: "Reach: self—"},
		{name: "unknown", reach: rbac.Reach{Raw: json.RawMessage(`"tenant"`), Value: "tenant"}, want: "Reach: tenant—", wantIncomplete: true},
		{name: "not stated", reach: rbac.Reach{}, want: "The server did not say", wantIncomplete: true},
		{name: "an empty string", reach: rbac.Reach{Raw: json.RawMessage(`""`)}, want: "Reach: (empty)—", wantIncomplete: true},
		{name: "a control sequence in an unknown value is stripped", reach: rbac.Reach{Raw: json.RawMessage(`"x\u001b[31m\nred"`), Value: "x\x1b[31m\nred"}, want: "Reach: x", wantIncomplete: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			line := reachLine(tt.reach)

			assert.Contains(t, line, tt.want)
			assert.NotContains(t, line, "your groups")
			assert.NotContains(t, line, "\x1b")
			assert.NotContains(t, line, "\n")
			if tt.wantIncomplete {
				assert.Contains(t, line, "may not be complete")
			} else {
				assert.NotContains(t, line, "may not be complete")
			}
		})
	}
}

// The bound rung is also a direct Application maintainer's, who audits no group: the line must
// describe the rows, not claim a binding the reader may not hold.
func TestReachLine_BoundClaimsNoAuditorBinding(t *testing.T) {
	t.Parallel()
	line := reachLine(rbac.Reach{Raw: json.RawMessage(`"bound"`), Value: rbac.ReachBound})

	assert.NotContains(t, line, "you hold auditor")
	assert.NotContains(t, line, "your auditor")
}

func TestNoticesToRepeat(t *testing.T) {
	t.Parallel()
	org := rbac.Reach{Raw: json.RawMessage(`"org"`), Value: rbac.ReachOrg}
	bound := rbac.Reach{Raw: json.RawMessage(`"bound"`), Value: rbac.ReachBound}
	tests := []struct {
		name        string
		reach       rbac.Reach
		paged       bool
		wantLine    bool
		wantWarning bool
	}{
		{name: "org on screen", reach: org},
		{name: "org paged", reach: org, paged: true},
		{name: "bound on screen", reach: bound},
		{name: "bound paged", reach: bound, paged: true, wantLine: true},
		{name: "self paged", reach: rbac.Reach{Raw: json.RawMessage(`"self"`), Value: rbac.ReachSelf}, paged: true, wantLine: true},
		{name: "unknown paged", reach: rbac.Reach{Raw: json.RawMessage(`"tenant"`), Value: "tenant"}, paged: true, wantLine: true},
		{name: "not stated paged", reach: rbac.Reach{}, paged: true, wantLine: true},
		{name: "org that changed on screen", reach: rbac.Reach{Raw: org.Raw, Value: org.Value, Changed: true}},
		{name: "org that changed paged", reach: rbac.Reach{Raw: org.Raw, Value: org.Value, Changed: true}, paged: true, wantWarning: true},
		{name: "bound that changed paged", reach: rbac.Reach{Raw: bound.Raw, Value: bound.Value, Changed: true}, paged: true, wantLine: true, wantWarning: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			line, warning := noticesToRepeat(tt.reach, tt.paged)

			assert.Equal(t, tt.wantLine, line, "repeat the reach line")
			assert.Equal(t, tt.wantWarning, warning, "repeat the warning")
		})
	}
}
