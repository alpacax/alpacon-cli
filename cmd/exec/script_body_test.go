package exec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scriptBodyLine = "alpacon exec -u root --upload --file /path/to/script.sh --file-from ./script.sh prod"

func TestScriptBodyAction(t *testing.T) {
	t.Parallel()

	signal := &event.ScriptBodySuggestion{Signal: "embedded_script"}

	action, ok := scriptBodyAction(RemoteExecArgs{Server: "srv", Username: "root", Groupname: "wheel", WorkSessionID: "ws-1"}, signal)
	require.True(t, ok)
	assert.Equal(t, "alpacon exec -u root -g wheel --work-session ws-1 --upload --file /path/to/script.sh --file-from ./script.sh srv", action.Command)
	assert.Contains(t, action.Description, "cancel it in the Alpacon console")
	assert.Contains(t, action.Description, "--overwrite")
	assert.NotContains(t, action.Description, "--env values")

	action, ok = scriptBodyAction(RemoteExecArgs{Server: "srv", Env: map[string]string{"DB_URL": ""}}, signal)
	require.True(t, ok, "--env still gets the hint, with a warning")
	assert.Contains(t, action.Description, "--env values cannot ride the file lane")

	_, ok = scriptBodyAction(RemoteExecArgs{Server: "srv"}, nil)
	assert.False(t, ok, "no suggestion")
	_, ok = scriptBodyAction(RemoteExecArgs{Server: "srv", File: &FileExecArgs{Path: "/tmp/x.sh"}}, signal)
	assert.False(t, ok, "already on the file lane")
}

const scriptBodyField = `,"script_body_suggestion":{"signal":"embedded_script"}`

// A held line the server says carries a script body gets the --upload --file
// template, in both output formats.
func TestExecHeldScriptBodyPrintsTheUploadTemplate(t *testing.T) {
	ts := newHeldScriptServer(scriptBodyField)
	t.Cleanup(ts.Close)
	line := []string{"root@prod", "--", "python3", "-c", "print(1)"}

	t.Run("table", func(t *testing.T) {
		_, stderr, exitCode := runExecHelper(t, ts.URL, line...)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		assert.Contains(t, stderr, "  "+scriptBodyLine+"  # ")
	})
	t.Run("json", func(t *testing.T) {
		stdout, _, exitCode := runExecHelper(t, ts.URL, append([]string{"--output", "json"}, line...)...)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		var got pendingApprovalSignal
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(stdout, "PASS\n")), &got), "stdout: %s", stdout)
		require.NotEmpty(t, got.NextActions)
		assert.Equal(t, scriptBodyLine, got.NextActions[len(got.NextActions)-1].Command)
	})
}

// The server's file suggestion wins: rendered, it replaces the template; set but
// unrenderable, it is still the server's answer, so no template either.
func TestExecHeldScriptBodyDefersToAFileSuggestion(t *testing.T) {
	line := []string{"root@prod", "--", "bash", "/opt/deploy.sh", "--fast"}

	t.Run("renderable", func(t *testing.T) {
		ts := newHeldScriptServer(`,"file_execution_suggestion":{"path":"/opt/deploy.sh","interpreter":"/bin/bash","args":["--fast"]}` + scriptBodyField)
		defer ts.Close()
		_, stderr, exitCode := runExecHelper(t, ts.URL, line...)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		assert.Contains(t, stderr, "--from-server")
		assert.NotContains(t, stderr, "--upload --file")
	})
	t.Run("unrenderable", func(t *testing.T) {
		ts := newHeldScriptServer(`,"file_execution_suggestion":{"path":"relative.sh","interpreter":"/bin/bash","args":[]}` + scriptBodyField)
		defer ts.Close()
		_, stderr, exitCode := runExecHelper(t, ts.URL, line...)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		assert.NotContains(t, stderr, "--from-server")
		assert.NotContains(t, stderr, "--upload --file")
	})
}

// Without the signal, an older server's output is unchanged.
func TestExecHeldWithoutScriptBodyKeepsTodaysHint(t *testing.T) {
	for name, fields := range map[string]string{"null": `,"script_body_suggestion":null`, "absent": ""} {
		t.Run(name, func(t *testing.T) {
			ts := newHeldScriptServer(fields)
			defer ts.Close()
			_, stderr, exitCode := runExecHelper(t, ts.URL, "root@prod", "--", "python3", "-c", "print(1)")
			assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
			assert.Contains(t, stderr, "alpacon exec logs cmd-1")
			assert.NotContains(t, stderr, "--upload --file")
		})
	}
}
