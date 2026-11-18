package approval

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fileExecutionFixture = `{
  "id": "apr-1",
  "request_type": "command_exec",
  "status": "pending",
  "metadata": {
    "command": {
      "command_id": "c-1",
      "line": "/bin/sh /tmp/deploy.sh --env prod",
      "file_execution": {
        "content": "#!/bin/sh\necho hello\n",
        "sha256": "abc123",
        "path": "/tmp/deploy.sh",
        "interpreter": "/bin/sh",
        "args": ["--env", "prod"],
        "runas": "deploy",
        "runas_group": null,
        "reuse_days": 30,
        "grant_days": 14
      }
    }
  }
}`

func decodeFixture(t *testing.T, body string) *approvalapi.ApprovalRequest {
	t.Helper()
	var req approvalapi.ApprovalRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return &req
}

func rowMap(rows []describeRow) map[string]string {
	m := map[string]string{}
	for _, r := range rows {
		m[strings.TrimSpace(r.Field)] = r.Value
	}
	return m
}

func TestFileExecutionRows(t *testing.T) {
	t.Parallel()
	fe := fileExecutionOf(decodeFixture(t, fileExecutionFixture))
	require.NotNil(t, fe)

	m := rowMap(fileExecutionRows(fe))
	assert.Equal(t, "/bin/sh", m["Interpreter"])
	assert.Equal(t, "/tmp/deploy.sh", m["Path"])
	assert.Equal(t, "--env prod", m["Arguments"])
	assert.Equal(t, "deploy", m["Run as"])
	assert.Equal(t, "abc123", m["SHA-256"])
	assert.Equal(t, "30", m["Proposed reuse days"])
	assert.Equal(t, "14", m["Reuse days if approved"])
	assert.NotContains(t, m, "Run as group")

	script, ok := fileExecutionScript(fe, false)
	require.True(t, ok)
	assert.Contains(t, script, "echo hello")
}

func TestFileExecutionOmitsAbsentReuseDays(t *testing.T) {
	t.Parallel()
	fe := fileExecutionOf(decodeFixture(t, `{"metadata":{"command":{"file_execution":{"content":"x","sha256":"h","path":"/p","interpreter":"/bin/sh","args":null,"reuse_days":null}}}}`))
	require.NotNil(t, fe)
	m := rowMap(fileExecutionRows(fe))
	assert.NotContains(t, m, "Proposed reuse days")
	assert.NotContains(t, m, "Reuse days if approved")
}

func TestFileExecutionScriptWithheld(t *testing.T) {
	t.Parallel()
	fe := fileExecutionOf(decodeFixture(t, `{"metadata":{"command":{"file_execution":{"content":null,"sha256":"h","path":"/p","interpreter":"/bin/sh"}}}}`))
	require.NotNil(t, fe)
	script, ok := fileExecutionScript(fe, false)
	require.True(t, ok)
	assert.Contains(t, script, "not included")
	assert.Contains(t, script, "SHA-256")
}

func TestFileExecutionStripsEscapes(t *testing.T) {
	t.Parallel()
	fe := fileExecutionOf(decodeFixture(t, `{"metadata":{"command":{"file_execution":{"content":"echo \u001b[2Kgone","sha256":"h","path":"/p\u001b[31m","interpreter":"/bin/sh","args":["a\u202eb"]}}}}`))
	require.NotNil(t, fe)
	for _, r := range fileExecutionRows(fe) {
		assert.NotContains(t, r.Value, "\x1b")
		assert.NotContains(t, r.Value, "\u202e")
	}
	script, _ := fileExecutionScript(fe, false)
	assert.NotContains(t, script, "\x1b")
}

func TestNumberedFileExecutionScript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "escapes are stripped without shifting a line",
			content: "echo \x1b[2Kgone\necho \u009bred\nok \u202edenied",
			want:    "Script (37 bytes):\n1 | echo gone\n2 | echo red\n3 | ok denied",
		},
		{
			name:    "numbers are padded to the widest",
			content: "a\nb\nc\nd\ne\nf\ng\nh\ni\nj",
			want:    "Script (19 bytes):\n 1 | a\n 2 | b\n 3 | c\n 4 | d\n 5 | e\n 6 | f\n 7 | g\n 8 | h\n 9 | i\n10 | j",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script, ok := fileExecutionScript(&approvalapi.FileExecution{Content: &tt.content}, true)
			require.True(t, ok)
			assert.Equal(t, tt.want, script)
		})
	}
}

func TestOtherRequestTypesHaveNoFileExecution(t *testing.T) {
	t.Parallel()
	assert.Nil(t, fileExecutionOf(decodeFixture(t, `{"id":"a","request_type":"sudo"}`)))
	// A command_exec request that is not a file execution.
	assert.Nil(t, fileExecutionOf(decodeFixture(t, `{"request_type":"command_exec","metadata":{"command":{"line":"ls"}}}`)))
}

func TestContextRows(t *testing.T) {
	t.Parallel()

	const decider = `{"metadata":{
	  "intent": "ship deploy v2",
	  "work_session": {"id": "ws-1", "title": "deploy v2", "description": "d", "status": "active", "assignee": {"id": "u-1", "name": "alice"}},
	  "policy_decision": {"policy_id": 1, "layer": "custom", "lane": "single_approver", "reason": "touches production"},
	  "command": {
	    "line": "systemctl restart app", "server_name": "my-server", "risk_level": "HIGH",
	    "runs_with_sudo": true, "purpose": "restart after deploy", "declared_agent": "claude",
	    "review_summary": "restarts a service", "tier2_reasoning": "matches the session purpose",
	    "factors": [{"type": "privilege", "detail": "runs as root"}, {"type": "scope", "detail": "one server"}]
	  }}}`

	tests := []struct {
		name string
		body string
		want []describeRow
	}{
		{
			name: "nil metadata gives none",
			body: `{}`,
			want: nil,
		},
		{
			name: "command request as a decider",
			body: decider,
			want: []describeRow{
				{"Work session", "deploy v2 (active, ws-1)"},
				{"Session purpose", "ship deploy v2"},
				{"Command", "systemctl restart app"},
				{"Server", "my-server"},
				{"Risk level", "HIGH"},
				{"Runs with sudo", "yes"},
				{"Purpose", "restart after deploy"},
				{"Declared agent", "claude"},
				{"Review summary", "restarts a service"},
				{"Reasoning", "matches the session purpose"},
				{"Risk factors", "privilege: runs as root; scope: one server"},
			},
		},
		{
			name: "command request as a requester",
			body: `{"metadata":{"intent":"ship deploy v2","work_session":null,"command":{
	  "line": "systemctl restart app", "server_name": "my-server", "risk_level": "HIGH",
	  "runs_with_sudo": true, "purpose": "restart after deploy", "declared_agent": "claude",
	  "review_summary": null, "tier2_reasoning": null, "factors": null}}}`,
			want: []describeRow{
				{"Session purpose", "ship deploy v2"},
				{"Command", "systemctl restart app"},
				{"Server", "my-server"},
				{"Risk level", "HIGH"},
				{"Runs with sudo", "yes"},
				{"Purpose", "restart after deploy"},
				{"Declared agent", "claude"},
			},
		},
		{
			name: "runs_with_sudo false leaves out the row",
			body: `{"metadata":{"command":{"line":"ls","runs_with_sudo":false}}}`,
			want: []describeRow{{"Command", "ls"}},
		},
		{
			name: "command request shows the run-as account",
			body: `{"metadata":{"command":{"line":"ls","server_name":"my-server","runas":"deploy","risk_level":"low"}}}`,
			want: []describeRow{
				{"Command", "ls"},
				{"Server", "my-server"},
				{"Run as", "deploy"},
				{"Risk level", "low"},
			},
		},
		{
			name: "command request with null runas leaves out the row",
			body: `{"metadata":{"command":{"line":"ls","server_name":"my-server","runas":null}}}`,
			want: []describeRow{
				{"Command", "ls"},
				{"Server", "my-server"},
			},
		},
		{
			name: "file execution leaves the run-as account to its own block",
			body: `{"metadata":{"command":{"line":"sh /tmp/a.sh","server_name":"my-server","runas":"deploy","file_execution":{"sha256":"abc","path":"/tmp/a.sh","interpreter":"/bin/sh","args":[],"runas":"deploy"}}}}`,
			want: []describeRow{
				{"Command", "sh /tmp/a.sh"},
				{"Server", "my-server"},
			},
		},
		{
			name: "sudo request shows the purpose after the run-as account",
			body: `{"metadata":{"sudo":{"command":"apt update","runas":"root","purpose":"patch openssl","server_name":"my-server"}}}`,
			want: []describeRow{
				{"Sudo command", "apt update"},
				{"Run as", "root"},
				{"Purpose", "patch openssl"},
				{"Server", "my-server"},
			},
		},
		{
			name: "sudo request with null purpose leaves out the row",
			body: `{"metadata":{"sudo":{"command":"apt update","runas":"root","purpose":null}}}`,
			want: []describeRow{
				{"Sudo command", "apt update"},
				{"Run as", "root"},
			},
		},
		{
			name: "sudo request shows the run-as account",
			body: `{"metadata":{"sudo":{"command":"apt update","runas":"root"}}}`,
			want: []describeRow{
				{"Sudo command", "apt update"},
				{"Run as", "root"},
			},
		},
		{
			name: "sudo request without runas leaves out the row",
			body: `{"metadata":{"sudo":{"command":"apt update"}}}`,
			want: []describeRow{{"Sudo command", "apt update"}},
		},
		{
			name: "sudo request with null runas leaves out the row",
			body: `{"metadata":{"sudo":{"command":"apt update","runas":null}}}`,
			want: []describeRow{{"Sudo command", "apt update"}},
		},
		{
			name: "sudo request with description_aligned false",
			body: `{"metadata":{"sudo":{"command":"apt update","server_name":"my-server","risk_level":"low","grant_status":"pending","declared_agent":"claude","tier2_reasoning":"routine","description_aligned":false}}}`,
			want: []describeRow{
				{"Sudo command", "apt update"},
				{"Server", "my-server"},
				{"Risk level", "low"},
				{"Grant status", "pending"},
				{"Declared agent", "claude"},
				{"Reasoning", "routine"},
				{"Matches session purpose", "no"},
			},
		},
		{
			name: "sudo request with description_aligned true",
			body: `{"metadata":{"sudo":{"command":"apt update","description_aligned":true}}}`,
			want: []describeRow{
				{"Sudo command", "apt update"},
				{"Matches session purpose", "yes"},
			},
		},
		{
			name: "sudo request with description_aligned null",
			body: `{"metadata":{"sudo":{"command":"apt update","description_aligned":null}}}`,
			want: []describeRow{{"Sudo command", "apt update"}},
		},
		{
			name: "work session change shows header and indented rows",
			body: `{"metadata":{"changes":{
	  "title": "deploy v3", "description": "extend", "scopes": ["command", "websh"],
	  "servers": [{"id": "s-1", "name": "my-server"}, {"id": "s-2", "name": "db-server"}],
	  "sudo_policies": [{"commands": ["apt update", "apt upgrade"], "reason": "r", "valid_until": null}, {"commands": ["reboot"], "reason": "r", "valid_until": "2030-01-01T00:00:00Z"}],
	  "target_account_username": "alice"}}}`,
			want: []describeRow{
				{"Requested changes", ""},
				{"  Title", "deploy v3"},
				{"  Description", "extend"},
				{"  Scopes", "command, websh"},
				{"  Servers", "my-server, db-server"},
				{"  Sudo policies", "apt update, apt upgrade; reboot"},
				{"  Target account", "alice"},
			},
		},
		{
			name: "names and hidden",
			body: `{"metadata":{"changes":{"servers":[{"id":"s-1","name":"my-server"}],"servers_hidden":2}}}`,
			want: []describeRow{{"Requested changes", ""}, {"  Servers", "my-server and 2 more"}},
		},
		{
			name: "all hidden",
			body: `{"metadata":{"changes":{"servers":[],"servers_hidden":1}}}`,
			want: []describeRow{{"Requested changes", ""}, {"  Servers", "1 server you cannot access"}},
		},
		{
			name: "zero hidden",
			body: `{"metadata":{"changes":{"servers":[],"servers_hidden":0}}}`,
			want: nil,
		},
		{
			name: "changes with no renderable key shows no header",
			body: `{"metadata":{"changes":{"scopes":[],"servers":null}}}`,
			want: nil,
		},
		{
			name: "factor and list items that sanitize to nothing show no row",
			body: "{\"metadata\":{\"command\":{\"factors\":[{\"type\":\"\\u001b[0m\",\"detail\":\"\"}]},\"changes\":{\"scopes\":[\"\",\"\\u001b[0m\"],\"servers\":[{\"id\":\"s-1\",\"name\":\"\"}],\"sudo_policies\":[{\"commands\":[]}]}}}",
			want: nil,
		},
		{
			name: "work session with no title keeps status and id",
			body: `{"metadata":{"work_session":{"id":"ws-1","title":null,"status":"active"}}}`,
			want: []describeRow{{"Work session", "(active, ws-1)"}},
		},
		{
			name: "work session title that sanitizes to nothing leaves no leading space",
			body: "{\"metadata\":{\"work_session\":{\"id\":\"ws-1\",\"title\":\"\\u001b[0m\",\"status\":\"active\"}}}",
			want: []describeRow{{"Work session", "(active, ws-1)"}},
		},
		{
			name: "work session title ending in a stray escape keeps the status intact",
			body: "{\"metadata\":{\"work_session\":{\"id\":\"ws-1\",\"title\":\"deploy\\u001b\",\"status\":\"active\"}}}",
			want: []describeRow{{"Work session", "deploy (active, ws-1)"}},
		},
		{
			name: "work session title ending in an unfinished CSI keeps the status intact",
			body: "{\"metadata\":{\"work_session\":{\"id\":\"ws-1\",\"title\":\"deploy\\u001b[\",\"status\":\"active\"}}}",
			want: []describeRow{{"Work session", "deploy (active, ws-1)"}},
		},
		{
			name: "escape sequence in a value is stripped",
			body: "{\"metadata\":{\"command\":{\"line\":\"ls \\u001b[31mred\\u001b[0m\"}}}",
			want: []describeRow{{"Command", "ls red"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := decodeFixture(t, tt.body)

			got := contextRows(req.Metadata)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDescribeRowsPutContextBetweenBaseAndFileExecution(t *testing.T) {
	t.Parallel()
	req := decodeFixture(t, `{"id":"apr-1","request_type":"command_exec","status":"pending","added_at":"2030-01-01T00:00:00Z","metadata":{"intent":"deploy v2","command":{"line":"ls","file_execution":{"sha256":"abc","path":"/tmp/a.sh","interpreter":"/bin/sh","args":[]}}}}`)

	rows := describeRows(req, fileExecutionOf(req))

	var fields []string
	for _, r := range rows {
		fields = append(fields, r.Field)
	}
	assert.Equal(t, []string{
		"ID", "Type", "Status", "Request", "Description", "Requested by", "Reviewed by", "Reviewed at", "Added at",
		"Session purpose", "Command",
		"File execution", "  Interpreter", "  Path", "  Arguments", "  SHA-256",
	}, fields)
}

func TestDescribeRowsShowFileExecutionRunAsOnce(t *testing.T) {
	t.Parallel()
	req := decodeFixture(t, `{"id":"apr-1","request_type":"command_exec","added_at":"2030-01-01T00:00:00Z","metadata":{"command":{"line":"sh /tmp/a.sh","runas":"deploy","file_execution":{"sha256":"abc","path":"/tmp/a.sh","interpreter":"/bin/sh","args":[],"runas":"deploy"}}}}`)

	var runAs []describeRow
	for _, r := range describeRows(req, fileExecutionOf(req)) {
		if strings.TrimSpace(r.Field) == "Run as" {
			runAs = append(runAs, r)
		}
	}

	assert.Equal(t, []describeRow{{"  Run as", "deploy"}}, runAs)
}

func TestApprovalDescribeJSONPreservesSudoRunAs(t *testing.T) {
	approvalDescribeCmd := newApprovalDescribeCmd(client.NewAlpaconAPIClient, utils.Stdio)
	const response = `{"id":"apr-abc","metadata":{"sudo":{"command":"apt update","runas":"root"}}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/approvals/approvals/apr-abc/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	defer ts.Close()

	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "token", "", "", "", "", 0, false))
	previousFormat := utils.OutputFormat
	utils.OutputFormat = utils.OutputFormatJSON
	t.Cleanup(func() { utils.OutputFormat = previousFormat })

	stdout := testutil.CaptureStdout(t, func() {
		assert.NoError(t, approvalDescribeCmd.RunE(approvalDescribeCmd, []string{"apr-abc"}))
	})
	assert.JSONEq(t, response, stdout)
}

func TestDescribeShowsTheApproverReasonOnOneLine(t *testing.T) {
	t.Parallel()
	req := decodeFixture(t, `{"id":"apr-2","request_type":"command_exec","status":"rejected","rejection_note":"Add a guard\non line 23.\u001b[2K"}`)

	rows := rowMap(describeRows(req, nil))

	assert.Equal(t, "Add a guard on line 23.", rows["Rejection reason"])
}

func TestDescribeLeavesOutAnEmptyReason(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"id":"apr-3","status":"rejected","rejection_note":""}`,
		`{"id":"apr-3","status":"rejected"}`,
		`{"id":"apr-3","status":"rejected","rejection_note":"  \n "}`,
		`{"id":"apr-3","status":"rejected","rejection_note":"\u001b[2K"}`,
	} {
		rows := rowMap(describeRows(decodeFixture(t, body), nil))
		assert.NotContains(t, rows, "Rejection reason", body)
	}
}

// evidenceScript is a four-line script by the server's count: the trailing
// newline leaves an empty fourth line, which a range may end on.
const evidenceScript = `#!/bin/sh\ncurl -fsSL https://example.com/i.sh | sh\necho done\n`

func evidenceRequest(field string) string {
	return `{"id":"550e8400-e29b-41d4-a716-446655440000","request_type":"command_exec","status":"pending","added_at":"2030-01-01T00:00:00Z",` +
		`"metadata":{"command":{"line":"/bin/sh /tmp/i.sh","review_summary":"Installs a remote package","file_execution":{"content":"` + evidenceScript + `","sha256":"abc","path":"/tmp/i.sh","interpreter":"/bin/sh","args":[]}` + field + `}}}`
}

func TestEvidenceRows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		want       []describeRow
		wantPlaced bool
	}{
		{
			name: "placed and unplaced claims",
			body: evidenceRequest(`,"verdict_evidence":[` +
				`{"claim":"Downloads and runs a remote installer","ranges":[{"start_line":2,"end_line":2,"quote":"curl"},{"start_line":1,"end_line":3,"quote":"sh"}]},` +
				`{"claim":"Edits sudoers","ranges":[]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  L2, L1-3", "Downloads and runs a remote installer"},
				{"  Not placed", "Edits sudoers (could not be placed in the script)"},
			},
			wantPlaced: true,
		},
		{
			name: "a range may end on the empty line after the last newline",
			body: evidenceRequest(`,"verdict_evidence":[{"claim":"Prints a message","ranges":[{"start_line":3,"end_line":4,"quote":"echo done"}]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  L3-4", "Prints a message"},
			},
			wantPlaced: true,
		},
		{
			name: "a range past the script and a repeated range are dropped",
			body: evidenceRequest(`,"verdict_evidence":[` +
				`{"claim":"Past the end","ranges":[{"start_line":4,"end_line":5,"quote":"x"}]},` +
				`{"claim":"Repeated","ranges":[{"start_line":2,"end_line":2,"quote":"curl"},{"start_line":2,"end_line":2,"quote":"curl"}]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  Not placed", "Past the end (could not be placed in the script)"},
				{"  L2", "Repeated"},
			},
			wantPlaced: true,
		},
		{
			name: "two claims may cite the same range",
			body: evidenceRequest(`,"verdict_evidence":[` +
				`{"claim":"Downloads an installer","ranges":[{"start_line":2,"end_line":2,"quote":"curl"}]},` +
				`{"claim":"Pipes it to a shell","ranges":[{"start_line":2,"end_line":2,"quote":"| sh"}]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  L2", "Downloads an installer"},
				{"  L2", "Pipes it to a shell"},
			},
			wantPlaced: true,
		},
		{
			name: "a range past the script drops alone and the claim stays placed",
			body: evidenceRequest(`,"verdict_evidence":[{"claim":"Partly past the end","ranges":[{"start_line":2,"end_line":2,"quote":"curl"},{"start_line":4,"end_line":5,"quote":"x"}]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  L2", "Partly past the end"},
			},
			wantPlaced: true,
		},
		{
			name: "only unplaced claims",
			body: evidenceRequest(`,"verdict_evidence":[{"claim":"Edits sudoers","ranges":[]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  Not placed", "Edits sudoers (could not be placed in the script)"},
			},
		},
		{
			name: "a withheld script places nothing",
			body: `{"metadata":{"command":{"file_execution":{"content":null,"sha256":"abc","path":"/tmp/i.sh","interpreter":"/bin/sh"},` +
				`"verdict_evidence":[{"claim":"Downloads","ranges":[{"start_line":1,"end_line":1,"quote":"x"}]}]}}}`,
			want: []describeRow{
				{"AI evidence", ""},
				{"  Not placed", "Downloads (could not be placed in the script)"},
			},
		},
		{
			name: "claim text is folded onto one line and stripped of escapes",
			body: evidenceRequest(`,"verdict_evidence":[{"claim":"Runs\n\u001b[2Kcurl \u202epiped to sh","ranges":[{"start_line":2,"end_line":2,"quote":"curl"}]},{"claim":"\u001b[2K","ranges":[]}]`),
			want: []describeRow{
				{"AI evidence", ""},
				{"  L2", "Runs curl piped to sh"},
			},
			wantPlaced: true,
		},
		{name: "absent", body: evidenceRequest(``)},
		{name: "null", body: evidenceRequest(`,"verdict_evidence":null`)},
		{name: "empty", body: evidenceRequest(`,"verdict_evidence":[]`)},
		{name: "malformed", body: evidenceRequest(`,"verdict_evidence":"oops"`)},
		{name: "a request with no command", body: `{"request_type":"sudo","metadata":{"sudo":{"command":"apt update"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows, placed := evidenceRows(decodeFixture(t, tt.body))
			assert.Equal(t, tt.want, rows)
			assert.Equal(t, tt.wantPlaced, placed)
		})
	}
}

func TestApprovalDescribeEndsTheTableWithTheEvidence(t *testing.T) {
	t.Parallel()
	stdout := runDescribe(t, evidenceRequest(`,"verdict_evidence":[{"claim":"Edits sudoers","ranges":[]}]`))

	assert.Regexp(t, `(?m)^  SHA-256 +abc\nAI evidence *\n  Not placed +Edits sudoers \(could not be placed in the script\)\n\nScript \(61 bytes\):\n`, stdout)
}

func runDescribe(t *testing.T, body string) string {
	t.Helper()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/approvals/approvals/550e8400-e29b-41d4-a716-446655440000/": {Status: http.StatusOK, Body: body},
	})
	stdout, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command { return newApprovalDescribeCmd(ts.Factory(), ios) }, "550e8400-e29b-41d4-a716-446655440000")
	require.Equal(t, 0, code, stderr)
	return stdout
}

func TestApprovalDescribeNumbersTheScriptBesidePlacedEvidence(t *testing.T) {
	t.Parallel()
	stdout := runDescribe(t, evidenceRequest(`,"verdict_evidence":[`+
		`{"claim":"Downloads and runs a remote installer","ranges":[{"start_line":2,"end_line":2,"quote":"curl"}]},`+
		`{"claim":"Edits sudoers","ranges":[]}]`))

	assert.Regexp(t, `(?m)^AI evidence\s*$`, stdout)
	assert.Regexp(t, `(?m)^  L2 +Downloads and runs a remote installer$`, stdout)
	assert.Regexp(t, `(?m)^  Not placed +Edits sudoers \(could not be placed in the script\)$`, stdout)
	assert.Contains(t, stdout, "\n\nScript (61 bytes):\n"+
		"1 | #!/bin/sh\n"+
		"2 | curl -fsSL https://example.com/i.sh | sh\n"+
		"3 | echo done\n"+
		"4 |\n")
}

func TestApprovalDescribeWithoutEvidenceIsUnchanged(t *testing.T) {
	t.Parallel()
	baseline := runDescribe(t, evidenceRequest(``))
	assert.NotContains(t, baseline, "AI evidence")
	assert.Contains(t, baseline, "\n\nScript (61 bytes):\n#!/bin/sh\ncurl -fsSL https://example.com/i.sh | sh\necho done\n\n")

	tests := []struct {
		name  string
		field string
	}{
		{name: "null", field: `,"verdict_evidence":null`},
		{name: "empty", field: `,"verdict_evidence":[]`},
		{name: "malformed field", field: `,"verdict_evidence":{"claim":"x"}`},
		{name: "malformed claims", field: `,"verdict_evidence":["x",{"claim":7,"ranges":[]},{"claim":"no ranges"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, baseline, runDescribe(t, evidenceRequest(tt.field)))
		})
	}
}

func TestApprovalDescribeLeavesTheScriptUnnumberedWhenNothingIsPlaced(t *testing.T) {
	t.Parallel()
	stdout := runDescribe(t, evidenceRequest(`,"verdict_evidence":[{"claim":"Edits sudoers","ranges":[{"start_line":9,"end_line":9,"quote":"x"}]}]`))

	assert.Regexp(t, `(?m)^  Not placed +Edits sudoers \(could not be placed in the script\)$`, stdout)
	assert.Contains(t, stdout, "\n\nScript (61 bytes):\n#!/bin/sh\ncurl -fsSL https://example.com/i.sh | sh\necho done\n\n")
}

func TestApprovalDescribeJSONKeepsTheEvidenceAsSent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		field string
	}{
		{name: "present", field: `,"verdict_evidence":[{"claim":"Edits sudoers","ranges":[{"start_line":2,"end_line":2,"quote":"curl"}]},{"claim":"Unlocated","ranges":[]}]`},
		{name: "null", field: `,"verdict_evidence":null`},
		{name: "absent", field: ``},
		{name: "malformed", field: `,"verdict_evidence":"oops"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := evidenceRequest(tt.field)
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/approvals/approvals/550e8400-e29b-41d4-a716-446655440000/": {Status: http.StatusOK, Body: body},
			})

			stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return newApprovalDescribeCmd(ts.Factory(), ios) }, "550e8400-e29b-41d4-a716-446655440000")

			require.Equal(t, 0, code, stderr)
			assert.JSONEq(t, body, stdout)
		})
	}
}

func TestFileExecutionScriptKeepsTabs(t *testing.T) {
	t.Parallel()
	content := "#!/bin/sh\nif true; then\n\techo\tdone\nfi\n"
	header := fmt.Sprintf("Script (%d bytes):\n", len(content))
	tests := []struct {
		name     string
		numbered bool
		want     string
	}{
		{name: "unnumbered", want: header + content},
		{name: "numbered", numbered: true, want: header + "1 | #!/bin/sh\n2 | if true; then\n3 | \techo\tdone\n4 | fi\n5 |"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script, ok := fileExecutionScript(&approvalapi.FileExecution{Content: &content}, tt.numbered)
			require.True(t, ok)
			assert.Equal(t, tt.want, script)
		})
	}
}

func TestSanitizeScript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		content     string
		want        string
		wantAltered bool
	}{
		{name: "clean", content: "echo done\n", want: "echo done\n"},
		{name: "tabs", content: "\techo\tdone\n", want: "\techo\tdone\n"},
		{name: "crlf line endings", content: "echo a\r\necho b\r\n", want: "echo a\necho b\n"},
		{name: "escape sequence", content: "echo \x1b[31mred\x1b[0m\n", want: "echo red\n", wantAltered: true},
		{name: "lone carriage return", content: "echo a\recho b\n", want: "echo aecho b\n", wantAltered: true},
		{name: "format character", content: "echo a\u202eb\n", want: "echo ab\n", wantAltered: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clean, altered := sanitizeScript(tt.content)
			assert.Equal(t, tt.want, clean)
			assert.Equal(t, tt.wantAltered, altered)
		})
	}
}

// scriptRequest is a file execution approval request whose script is content.
func scriptRequest(t *testing.T, content string) string {
	t.Helper()
	encoded, err := json.Marshal(content)
	require.NoError(t, err)
	return `{"id":"550e8400-e29b-41d4-a716-446655440000","request_type":"command_exec","status":"pending",` +
		`"metadata":{"command":{"line":"/bin/sh /tmp/i.sh","file_execution":{"content":` + string(encoded) +
		`,"sha256":"abc","path":"/tmp/i.sh","interpreter":"/bin/sh","args":[]}}}}`
}

func runDescribeStreams(t *testing.T, body string) (stdout, stderr string) {
	t.Helper()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/approvals/approvals/550e8400-e29b-41d4-a716-446655440000/": {Status: http.StatusOK, Body: body},
	})
	stdout, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command { return newApprovalDescribeCmd(ts.Factory(), ios) }, "550e8400-e29b-41d4-a716-446655440000")
	require.Equal(t, 0, code, stderr)
	return stdout, stderr
}

const scriptAlteredWarning = "control characters from the server were removed from the text below."

func TestApprovalDescribeWarnsAboveAnAlteredScript(t *testing.T) {
	t.Parallel()
	content := "echo \x1b[31mred\x1b[0m\n"
	stdout, _ := runDescribeStreams(t, scriptRequest(t, content))

	assert.Contains(t, stdout, fmt.Sprintf("\n\nWarning: %s\nScript (%d bytes):\necho red\n\n", scriptAlteredWarning, len(content)))
	assert.NotContains(t, stdout, "\x1b[31m")
}

func TestApprovalDescribeDoesNotWarnAboutACleanScript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "tabs", content: "if true; then\n\techo\tdone\nfi\n", want: "if true; then\n\techo\tdone\nfi\n"},
		{name: "crlf line endings", content: "echo a\r\necho b\r\n", want: "echo a\necho b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr := runDescribeStreams(t, scriptRequest(t, tt.content))
			assert.Contains(t, stdout, fmt.Sprintf("\n\nScript (%d bytes):\n%s\n", len(tt.content), tt.want))
			assert.NotContains(t, stdout, scriptAlteredWarning)
			assert.NotContains(t, stderr, scriptAlteredWarning)
		})
	}
}
