package exec

import (
	"testing"

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
