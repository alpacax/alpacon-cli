package approval

import (
	"encoding/json"
	"strings"
	"testing"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape of the command_exec detail payload for a file execution request.
const fileExecutionFixture = `{
  "id": "apr-1",
  "request_type": "command_exec",
  "status": "pending",
  "command": {
    "command_id": "c-1",
    "shell": "file",
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
	req := decodeFixture(t, `{"command":{"file_execution":{"content":"x","sha256":"h","path":"/p","interpreter":"/bin/sh","args":null,"reuse_days":null}}}`)
	m := rowMap(fileExecutionRows(fileExecutionOf(req)))
	assert.NotContains(t, m, "Proposed reuse days")
	assert.NotContains(t, m, "Reuse days if approved")
}

func TestFileExecutionScriptWithheld(t *testing.T) {
	t.Parallel()
	req := decodeFixture(t, `{"command":{"file_execution":{"content":null,"sha256":"h","path":"/p","interpreter":"/bin/sh"}}}`)
	script, ok := fileExecutionScript(fileExecutionOf(req))
	require.True(t, ok)
	assert.Contains(t, script, "not included")
	assert.Contains(t, script, "SHA-256")
}

func TestFileExecutionStripsEscapes(t *testing.T) {
	t.Parallel()
	req := decodeFixture(t, `{"command":{"file_execution":{"content":"echo \u001b[2Kgone","sha256":"h","path":"/p\u001b[31m","interpreter":"/bin/sh","args":["a\u202eb"]}}}`)
	fe := fileExecutionOf(req)
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
	assert.Nil(t, fileExecutionOf(decodeFixture(t, `{"request_type":"command_exec","command":{"line":"ls"}}`)))
}
