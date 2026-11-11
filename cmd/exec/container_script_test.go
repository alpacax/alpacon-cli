package exec

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestContainerScriptAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		parsed RemoteExecArgs
		want   bool
	}{
		{
			name:   "docker exec with a Django shell body",
			parsed: RemoteExecArgs{Server: "srv", Command: `/usr/bin/docker exec app /app/.venv/bin/python /app/manage.py shell -c 'print(1)'`},
			want:   true,
		},
		{
			name:   "kubectl exec with python -c",
			parsed: RemoteExecArgs{Server: "srv", Command: `sudo kubectl -n ns exec deploy/app -- python -c "print(1)"`},
			want:   true,
		},
		{
			name:   "kubectl exec with a heredoc",
			parsed: RemoteExecArgs{Server: "srv", Command: "kubectl exec -i pod -- python - <<'PY'\nprint(1)\nPY"},
			want:   true,
		},
		{
			name:   "container exec that runs a named management command",
			parsed: RemoteExecArgs{Server: "srv", Command: "docker exec app python manage.py migrate"},
			want:   false,
		},
		{
			name:   "kubectl -c selects a container, not a program body",
			parsed: RemoteExecArgs{Server: "srv", Command: "kubectl exec -c sidecar pod -- python script.py"},
			want:   false,
		},
		{
			name:   "docker -e sets an environment variable, not a program body",
			parsed: RemoteExecArgs{Server: "srv", Command: "docker exec -e KEY=value app python script.py"},
			want:   false,
		},
		{
			name:   "runtime options before an inline program",
			parsed: RemoteExecArgs{Server: "srv", Command: `docker exec -e KEY=value -w /app app python -c "print(1)"`},
			want:   true,
		},
		{
			name:   "a -- among the program's own arguments",
			parsed: RemoteExecArgs{Server: "srv", Command: `docker exec app python -c 'print(1)' -- --dry-run`},
			want:   true,
		},
		{
			name:   "exec inside the program body",
			parsed: RemoteExecArgs{Server: "srv", Command: `docker exec app python -c 'exec("print(1)")'`},
			want:   true,
		},
		{
			name:   "kubectl options, the pod, then --",
			parsed: RemoteExecArgs{Server: "srv", Command: `kubectl exec -c app pod -- python -c "print(1)"`},
			want:   true,
		},
		{
			name:   "kubectl options after the pod, then a named command",
			parsed: RemoteExecArgs{Server: "srv", Command: "kubectl exec pod -n ns -c app -- python manage.py migrate"},
			want:   false,
		},
		{
			name:   "kubectl options after the pod, then an inline program",
			parsed: RemoteExecArgs{Server: "srv", Command: `kubectl exec pod -n ns -c app -- python -c "print(1)"`},
			want:   true,
		},
		{
			name:   "inline program on the host, not in a container",
			parsed: RemoteExecArgs{Server: "srv", Command: `python3 -c "print(1)"`},
			want:   false,
		},
		{
			name:   "already on the file lane",
			parsed: RemoteExecArgs{Server: "srv", Command: `docker exec app sh -c 'ls'`, File: &FileExecArgs{Path: "/tmp/x.sh"}},
			want:   false,
		},
		{
			name:   "carried --env, which the file lane cannot take",
			parsed: RemoteExecArgs{Server: "srv", Command: `docker exec app sh -c 'ls'`, Env: map[string]string{"K": "v"}},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok := containerScriptAction(tt.parsed)
			assert.Equal(t, tt.want, ok)
		})
	}
}

// The template keeps the account, group and work session of the held command,
// and names the upload form, since the script does not exist on the host yet.
func TestContainerScriptActionKeepsRunAsFlags(t *testing.T) {
	t.Parallel()

	action, ok := containerScriptAction(RemoteExecArgs{
		Server:        "srv",
		Username:      "root",
		Groupname:     "wheel",
		WorkSessionID: "ws-1",
		Command:       `docker exec app sh -c 'ls'`,
	})

	assert.True(t, ok)
	assert.Equal(t, "alpacon exec -u root -g wheel --work-session ws-1 --upload --file /tmp/container-work.sh --file-from ./container-work.sh srv", action.Command)
	assert.Contains(t, action.Description, "cancel it in the Alpacon console")
}

// A held container command gets the script template only when the server sent
// no suggestion: one it sent that this CLI cannot render is still its answer.
func TestExecHeldContainerCommandTemplateWaitsForNoSuggestion(t *testing.T) {
	line := []string{"root@prod", "--", "docker", "exec", "app", "python", "-c", "print(1)"}

	t.Run("no suggestion", func(t *testing.T) {
		ts := newHeldScriptServer("")
		defer ts.Close()
		_, stderr, exitCode := runExecHelper(t, ts.URL, line...)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		assert.Contains(t, stderr, "--upload --file /tmp/container-work.sh")
	})
	t.Run("suggestion this CLI cannot render", func(t *testing.T) {
		ts := newHeldScriptServer(`{"path":"relative.sh","interpreter":"/bin/bash","args":[]}`)
		defer ts.Close()
		_, stderr, exitCode := runExecHelper(t, ts.URL, line...)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		assert.NotContains(t, stderr, "--upload --file /tmp/container-work.sh")
	})
}
