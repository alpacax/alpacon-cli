package approval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape of the command_exec detail payload for a file execution request.
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

	script, ok := fileExecutionScript(fe)
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
	script, ok := fileExecutionScript(fe)
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
	script, _ := fileExecutionScript(fe)
	assert.NotContains(t, script, "\x1b")
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
			name: "sudo request shows the run-as account",
			body: `{"metadata":{"sudo":{"command":"apt update","runas":"root"}}}`,
			want: []describeRow{
				{"Sudo command", "apt update"},
				{"  Run as", "root"},
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

func TestApprovalDescribeJSONPreservesSudoRunAs(t *testing.T) {
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
		approvalDescribeCmd.Run(approvalDescribeCmd, []string{"apr-abc"})
	})
	assert.JSONEq(t, response, stdout)
}
