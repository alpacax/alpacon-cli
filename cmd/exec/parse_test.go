package exec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseRemoteExecArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		expected RemoteExecArgs
	}{
		// ── Basic usage ─────────────────────────────────────────────
		{
			name: "simple command",
			args: []string{"prod-docker", "docker", "ps"},
			expected: RemoteExecArgs{
				Server:  "prod-docker",
				Command: "docker ps",
			},
		},
		{
			name: "single word command",
			args: []string{"server", "uptime"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "uptime",
			},
		},
		{
			name: "server only without command",
			args: []string{"my-server"},
			expected: RemoteExecArgs{
				Server:  "my-server",
				Command: "",
			},
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: RemoteExecArgs{},
		},

		// ── SSH-like user@host syntax ───────────────────────────────
		{
			name: "user@host syntax",
			args: []string{"root@prod-docker", "docker", "ps"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "prod-docker",
				Command:  "docker ps",
			},
		},
		{
			name: "complex hostname with user",
			args: []string{"deploy@web-server-01.example.com", "systemctl", "status", "nginx"},
			expected: RemoteExecArgs{
				Username: "deploy",
				Server:   "web-server-01.example.com",
				Command:  "systemctl status nginx",
			},
		},

		// ── Flag parsing (-u, -g) ───────────────────────────────────
		{
			name: "-u flag with space",
			args: []string{"-u", "admin", "server", "ls"},
			expected: RemoteExecArgs{
				Username: "admin",
				Server:   "server",
				Command:  "ls",
			},
		},
		{
			name: "--username=value flag",
			args: []string{"--username=admin", "server", "ls"},
			expected: RemoteExecArgs{
				Username: "admin",
				Server:   "server",
				Command:  "ls",
			},
		},
		{
			name: "-g flag with space",
			args: []string{"-g", "docker", "server", "docker", "ps"},
			expected: RemoteExecArgs{
				Groupname: "docker",
				Server:    "server",
				Command:   "docker ps",
			},
		},
		{
			name: "--groupname=value flag",
			args: []string{"--groupname=docker", "server", "ls"},
			expected: RemoteExecArgs{
				Groupname: "docker",
				Server:    "server",
				Command:   "ls",
			},
		},
		{
			name: "-uroot attached short flag",
			args: []string{"-uroot", "server", "ls"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "server",
				Command:  "ls",
			},
		},
		{
			name: "-gdocker attached short flag",
			args: []string{"-gdocker", "server", "ls"},
			expected: RemoteExecArgs{
				Groupname: "docker",
				Server:    "server",
				Command:   "ls",
			},
		},
		{
			name: "-uroot with -gdocker attached",
			args: []string{"-uroot", "-gdocker", "server", "uptime"},
			expected: RemoteExecArgs{
				Username:  "root",
				Groupname: "docker",
				Server:    "server",
				Command:   "uptime",
			},
		},
		{
			name: "both -u and -g flags",
			args: []string{"-u", "admin", "-g", "docker", "server", "uptime"},
			expected: RemoteExecArgs{
				Username:  "admin",
				Groupname: "docker",
				Server:    "server",
				Command:   "uptime",
			},
		},
		{
			name: "-u flag overrides user@host",
			args: []string{"-u", "override", "root@server", "ls"},
			expected: RemoteExecArgs{
				Username: "override",
				Server:   "server",
				Command:  "ls",
			},
		},
		{
			name: "user@host used when no -u flag",
			args: []string{"-g", "docker", "admin@server", "ls"},
			expected: RemoteExecArgs{
				Username:  "admin",
				Groupname: "docker",
				Server:    "server",
				Command:   "ls",
			},
		},

		// ── Double-dash separator (the core fix) ────────────────────
		{
			name: "-- separator basic",
			args: []string{"server", "--", "docker", "ps"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "docker ps",
			},
		},
		{
			name: "-- prevents remote -U from being parsed as alpacon -u",
			args: []string{"root@db-server", "--", "docker", "exec", "postgres", "psql", "-U", "myproject", "-d", "myproject"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "db-server",
				Command:  "docker exec postgres psql -U myproject -d myproject",
			},
		},
		{
			name: "-- with flags before separator",
			args: []string{"-u", "root", "-g", "dba", "db-server", "--", "psql", "-U", "postgres", "-d", "mydb"},
			expected: RemoteExecArgs{
				Username:  "root",
				Groupname: "dba",
				Server:    "db-server",
				Command:   "psql -U postgres -d mydb",
			},
		},
		{
			name: "-- with nothing after it",
			args: []string{"server", "--"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "",
			},
		},
		{
			name: "-- before server name",
			args: []string{"-u", "admin", "--", "server", "ls", "-la"},
			expected: RemoteExecArgs{
				Username: "admin",
				Server:   "server",
				Command:  "ls -la",
			},
		},

		// ── Remote commands with flags that look like alpacon flags ──
		{
			name: "remote -u without -- (no separator, swallowed as command arg)",
			args: []string{"server", "grep", "-u", "pattern", "/var/log/syslog"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "grep -u pattern /var/log/syslog",
			},
		},
		{
			name: "remote -g without -- (no separator, swallowed as command arg)",
			args: []string{"server", "id", "-g"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "id -g",
			},
		},
		{
			name: "remote --help as command arg",
			args: []string{"server", "--", "docker", "--help"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "docker --help",
			},
		},

		// ── Shell operators and special characters ──────────────────
		{
			name: "pipe operator",
			args: []string{"root@server", "ps", "aux", "|", "grep", "nginx"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "server",
				Command:  "ps aux | grep nginx",
			},
		},
		{
			name: "output redirection",
			args: []string{"server", "echo", "hello", ">", "/tmp/out.txt"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo hello > /tmp/out.txt",
			},
		},
		{
			name: "append redirection",
			args: []string{"server", "echo", "line", ">>", "/tmp/out.txt"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo line >> /tmp/out.txt",
			},
		},
		{
			name: "command chaining with &&",
			args: []string{"server", "cd", "/app", "&&", "make", "build"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "cd /app && make build",
			},
		},
		{
			name: "command chaining with semicolons",
			args: []string{"server", "echo", "start;", "sleep", "1;", "echo", "done"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo start; sleep 1; echo done",
			},
		},
		{
			name: "command substitution with backticks",
			args: []string{"server", "echo", "`hostname`"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo `hostname`",
			},
		},
		{
			name: "command substitution with $()",
			args: []string{"server", "echo", "$(date", "+%Y-%m-%d)"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo $(date +%Y-%m-%d)",
			},
		},
		{
			name: "environment variable reference",
			args: []string{"server", "echo", "$HOME"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo $HOME",
			},
		},
		{
			name: "quoted string with spaces (shell pre-split)",
			args: []string{"server", "echo", "hello world"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo 'hello world'",
			},
		},
		{
			name: "single-quoted arg preserved",
			args: []string{"server", "bash", "-c", "echo 'hello world'"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: `bash -c 'echo '\''hello world'\'''`,
			},
		},
		{
			name: "double-quoted arg preserved",
			args: []string{"server", "bash", "-c", `echo "hello world"`},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: `bash -c 'echo "hello world"'`,
			},
		},
		{
			name: "glob pattern",
			args: []string{"server", "ls", "/var/log/*.log"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "ls /var/log/*.log",
			},
		},
		{
			name: "curly brace expansion",
			args: []string{"server", "cp", "/etc/{nginx,apache2}/conf.d"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "cp /etc/{nginx,apache2}/conf.d",
			},
		},

		// ── Real-world complex commands ─────────────────────────────
		{
			name: "docker exec with psql flags via --",
			args: []string{"-u", "root", "db-server", "--", "docker", "exec", "-it", "postgres", "psql", "-U", "myproject", "-d", "myproject", "-c", "SELECT 1;"},
			expected: RemoteExecArgs{
				Username: "root",
				Server:   "db-server",
				Command:  "docker exec -it postgres psql -U myproject -d myproject -c 'SELECT 1;'",
			},
		},
		{
			name: "kubectl exec with flags via --",
			args: []string{"k8s-node", "--", "kubectl", "exec", "-n", "prod", "my-pod", "--", "cat", "/etc/config"},
			expected: RemoteExecArgs{
				Server:  "k8s-node",
				Command: "kubectl exec -n prod my-pod -- cat /etc/config",
			},
		},
		{
			name: "find with -name flag via --",
			args: []string{"server", "--", "find", "/var/log", "-name", "*.log", "-mtime", "-7"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "find /var/log -name *.log -mtime -7",
			},
		},
		{
			name: "tar with flags via --",
			args: []string{"server", "--", "tar", "-czf", "/tmp/backup.tar.gz", "-C", "/var/www", "."},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "tar -czf /tmp/backup.tar.gz -C /var/www .",
			},
		},
		{
			name: "awk with pattern",
			args: []string{"server", "awk", "{print $1}", "/var/log/access.log"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "awk '{print $1}' /var/log/access.log",
			},
		},
		{
			name: "sed with substitution",
			args: []string{"server", "sed", "-i", "s/old/new/g", "/etc/config.conf"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "sed -i s/old/new/g /etc/config.conf",
			},
		},
		{
			name: "curl with headers and URL",
			args: []string{"server", "--", "curl", "-s", "-H", "Authorization: Bearer token123", "http://localhost:8080/api/health"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "curl -s -H 'Authorization: Bearer token123' http://localhost:8080/api/health",
			},
		},
		{
			name: "multi-pipe command",
			args: []string{"server", "cat", "/var/log/access.log", "|", "grep", "ERROR", "|", "wc", "-l"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "cat /var/log/access.log | grep ERROR | wc -l",
			},
		},
		{
			name: "ssh tunnel-like colons in args are not treated as user@host:port",
			args: []string{"server", "echo", "host:8080"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "echo host:8080",
			},
		},
		{
			name: "server name containing colon is not parsed as user@host",
			args: []string{"user@host:8080", "ls"},
			expected: RemoteExecArgs{
				Server:  "user@host:8080",
				Command: "ls",
			},
		},
		// ── Edge cases ──────────────────────────────────────────────
		{
			name: "-u with only server, no command",
			args: []string{"-u", "admin", "server"},
			expected: RemoteExecArgs{
				Username: "admin",
				Server:   "server",
				Command:  "",
			},
		},
		{
			name:     "only -- with nothing else",
			args:     []string{"--"},
			expected: RemoteExecArgs{},
		},

		// ── --wait-approval flag ────────────────────────────────────
		{
			name: "wait-approval with space value",
			args: []string{"--wait-approval", "10m", "server", "ls"},
			expected: RemoteExecArgs{
				WaitApproval: 10 * time.Minute,
				Server:       "server",
				Command:      "ls",
			},
		},
		{
			name: "wait-approval with equals value",
			args: []string{"--wait-approval=30m", "server", "ls"},
			expected: RemoteExecArgs{
				WaitApproval: 30 * time.Minute,
				Server:       "server",
				Command:      "ls",
			},
		},
		{
			name: "wait-approval combined with wait",
			args: []string{"--wait", "--wait-approval", "10m", "server", "ls"},
			expected: RemoteExecArgs{
				Wait:         true,
				WaitApproval: 10 * time.Minute,
				Server:       "server",
				Command:      "ls",
			},
		},
		{
			name:     "wait-approval missing value",
			args:     []string{"--wait-approval"},
			expected: RemoteExecArgs{Err: "flag needs an argument: --wait-approval"},
		},
		{
			name:     "wait-approval explicit empty value",
			args:     []string{"--wait-approval=", "server", "ls"},
			expected: RemoteExecArgs{Err: `invalid --wait-approval value "": time: invalid duration ""`},
		},
		{
			name: "wait-approval invalid duration",
			args: []string{"--wait-approval", "10minutes", "server", "ls"},
			expected: RemoteExecArgs{
				Err: `invalid --wait-approval value "10minutes": time: unknown unit "minutes" in duration "10minutes"`,
			},
		},
		{
			name:     "wait-approval zero duration",
			args:     []string{"--wait-approval", "0s", "server", "ls"},
			expected: RemoteExecArgs{Err: `invalid --wait-approval value "0s": must be a positive duration`},
		},
		{
			name:     "wait-approval negative duration",
			args:     []string{"--wait-approval=-5m", "server", "ls"},
			expected: RemoteExecArgs{Err: `invalid --wait-approval value "-5m": must be a positive duration`},
		},
		{
			name: "wait-approval with detach is an error",
			args: []string{"--detach", "--wait-approval", "10m", "server", "ls"},
			expected: RemoteExecArgs{
				Err: "--wait-approval and --detach cannot be combined; --detach returns immediately and would ignore --wait-approval",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)

			assert.Equal(t, tt.expected.Username, result.Username, "Username")
			assert.Equal(t, tt.expected.Groupname, result.Groupname, "Groupname")
			assert.Equal(t, tt.expected.Server, result.Server, "Server")
			assert.Equal(t, tt.expected.Command, result.Command, "Command")
			assert.Equal(t, tt.expected.Wait, result.Wait, "Wait")
			assert.Equal(t, tt.expected.WaitApproval, result.WaitApproval, "WaitApproval")
			assert.Equal(t, tt.expected.Err, result.Err, "Err")
		})
	}
}

func TestWaitTimeout(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Duration(0), RemoteExecArgs{}.WaitTimeout())
	assert.Equal(t, 5*time.Minute, RemoteExecArgs{Wait: true}.WaitTimeout())
	assert.Equal(t, 10*time.Minute, RemoteExecArgs{WaitApproval: 10 * time.Minute}.WaitTimeout())
	// --wait-approval wins over bare --wait
	assert.Equal(t, 10*time.Minute, RemoteExecArgs{Wait: true, WaitApproval: 10 * time.Minute}.WaitTimeout())
}

func TestParseRemoteExecArgs_HelpFlag(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			result := ParseRemoteExecArgs([]string{flag, "server", "ls"})
			assert.True(t, result.ShowHelp, "ShowHelp should be true")
			assert.Empty(t, result.Server, "help flag should return empty result")
		})
	}
}

func TestParseRemoteExecArgs_WorkSessionFlag(t *testing.T) {
	t.Parallel()
	parsed := ParseRemoteExecArgs([]string{"--work-session", "ses-abc", "my-server", "ls"})
	assert.Equal(t, "ses-abc", parsed.WorkSessionID)
	assert.Equal(t, "my-server", parsed.Server)
	assert.Equal(t, "ls", parsed.Command)
}

func TestParseRemoteExecArgs_WorkSessionEqualForm(t *testing.T) {
	t.Parallel()
	parsed := ParseRemoteExecArgs([]string{"--work-session=ses-abc", "my-server", "ls"})
	assert.Equal(t, "ses-abc", parsed.WorkSessionID)
	assert.Equal(t, "my-server", parsed.Server)
	assert.Equal(t, "ls", parsed.Command)
}

func TestParseRemoteExecArgs_DoubleDashIgnoresWorkSession(t *testing.T) {
	t.Parallel()
	parsed := ParseRemoteExecArgs([]string{"my-server", "--", "ls", "--work-session", "fake"})
	assert.Equal(t, "", parsed.WorkSessionID)
	assert.Equal(t, "my-server", parsed.Server)
	assert.Contains(t, parsed.Command, "--work-session")
}

// TestParseRemoteExecArgs_ShellQuoting verifies that multi-word arguments
// (e.g. the script body in bash -c '...') are re-quoted after the OS shell
// strips the surrounding quotes during tokenization. Without this, strings.Join
// loses the arg boundary and the server's sh fails with a syntax error (issue #164).
func TestParseRemoteExecArgs_ShellQuoting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		args             []string
		expectedUsername string
		expectedServer   string
		expectedCommand  string
	}{
		{
			// Exact reproduction of issue #164 symptom 3.
			// User types: alpacon exec root@r770-1 bash -c 'for i in 1 2 3; do echo "line-$i"; done'
			// OS shell strips the outer single quotes; CLI receives the for-loop body as one token.
			name:             "bash -c for loop body (issue #164)",
			args:             []string{"root@r770-1", "bash", "-c", `for i in 1 2 3; do echo "line-$i"; done`},
			expectedUsername: "root",
			expectedServer:   "r770-1",
			expectedCommand:  `bash -c 'for i in 1 2 3; do echo "line-$i"; done'`,
		},
		{
			// Simpler variant: stdout check from issue #164 symptom 1.
			// User types: alpacon exec root@r770-1 bash -c 'echo hello-stdout'
			name:             "bash -c simple echo",
			args:             []string{"root@r770-1", "bash", "-c", "echo hello-stdout"},
			expectedUsername: "root",
			expectedServer:   "r770-1",
			expectedCommand:  "bash -c 'echo hello-stdout'",
		},
		{
			// Exit-code check from issue #164 symptom 2.
			// User types: alpacon exec root@r770-1 bash -c 'echo before-fail; exit 1'
			name:            "bash -c with exit code",
			args:            []string{"server", "bash", "-c", "echo before-fail; exit 1"},
			expectedServer:  "server",
			expectedCommand: "bash -c 'echo before-fail; exit 1'",
		},
		{
			// Non-bash interpreter with a multi-word script body.
			name:            "python3 -c with spaces",
			args:            []string{"server", "python3", "-c", "import sys; print(sys.version)"},
			expectedServer:  "server",
			expectedCommand: "python3 -c 'import sys; print(sys.version)'",
		},
		{
			// Args with no special characters must not be quoted (no behavior change).
			name:            "plain command — no quoting needed",
			args:            []string{"server", "docker", "ps"},
			expectedServer:  "server",
			expectedCommand: "docker ps",
		},
		{
			// Arg contains an internal single quote — must be escaped as '\''
			name:            "arg with internal single quote",
			args:            []string{"server", "bash", "-c", "echo it's done"},
			expectedServer:  "server",
			expectedCommand: `bash -c 'echo it'\''s done'`,
		},
		{
			// Single token with spaces — entire command passed as one quoted CLI argument,
			// e.g. alpacon exec server "ls -la /var/log". Must be returned unchanged
			// so the remote shell interprets it as a command with arguments.
			name:            "single-token command with spaces",
			args:            []string{"server", "ls -la /var/log"},
			expectedServer:  "server",
			expectedCommand: "ls -la /var/log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)
			assert.Equal(t, tt.expectedServer, result.Server, "Server")
			assert.Equal(t, tt.expectedUsername, result.Username, "Username")
			assert.Equal(t, tt.expectedCommand, result.Command, "Command")
		})
	}
}

func TestParseRemoteExecArgs_OutputFlag(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		expectedOutput string
		expectedServer string
		expectedCmd    string
	}{
		{
			name:           "--output json (space form)",
			args:           []string{"--output", "json", "my-server", "ls"},
			expectedOutput: "json",
			expectedServer: "my-server",
			expectedCmd:    "ls",
		},
		{
			name:           "--output=table (equals form)",
			args:           []string{"--output=table", "my-server", "ls"},
			expectedOutput: "table",
			expectedServer: "my-server",
			expectedCmd:    "ls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)
			assert.Equal(t, tt.expectedOutput, result.OutputFormat)
			assert.Equal(t, tt.expectedServer, result.Server)
			assert.Equal(t, tt.expectedCmd, result.Command)
		})
	}
}

func TestParseRemoteExecArgs_OutputFlagMissingValue(t *testing.T) {
	t.Parallel()
	result := ParseRemoteExecArgs([]string{"--output"})
	assert.NotEmpty(t, result.Err)
}

func TestParseRemoteExecArgs_OutputFlagEmptyValue(t *testing.T) {
	t.Parallel()
	result := ParseRemoteExecArgs([]string{"--output="})
	assert.NotEmpty(t, result.Err)
}

func TestParseRemoteExecArgs_DetachFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		wantDetach  bool
		wantServer  string
		wantCommand string
	}{
		{
			name:        "--detach before server",
			args:        []string{"--detach", "server", "apt", "upgrade"},
			wantDetach:  true,
			wantServer:  "server",
			wantCommand: "apt upgrade",
		},
		{
			name:        "--detach combined with -u",
			args:        []string{"--detach", "-u", "root", "server", "apt", "upgrade"},
			wantDetach:  true,
			wantServer:  "server",
			wantCommand: "apt upgrade",
		},
		{
			name:        "no --detach flag",
			args:        []string{"server", "ls"},
			wantDetach:  false,
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			name:        "--detach after server is treated as command arg",
			args:        []string{"server", "--detach"},
			wantDetach:  false,
			wantServer:  "server",
			wantCommand: "--detach",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)
			assert.Equal(t, tt.wantDetach, result.Detach, "Detach")
			assert.Equal(t, tt.wantServer, result.Server, "Server")
			assert.Equal(t, tt.wantCommand, result.Command, "Command")
		})
	}
}

func TestParseRemoteExecArgs_EnvFlag(t *testing.T) {
	t.Setenv("DB_PASS", "hunter2")

	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantEnv     map[string]string
		absentEnv   []string
		wantServer  string
		wantCommand string
	}{
		{
			name:        "--env=KEY=VALUE literal form",
			args:        []string{"--env=DB_PASS=literal", "server", "ls"},
			wantEnv:     map[string]string{"DB_PASS": "literal"},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			// Exact command match proves the value never lands on the command line.
			name:        "--env=KEY reads shell value and keeps it off the command",
			args:        []string{"--env=DB_PASS", "server", "echo", "hi"},
			wantEnv:     map[string]string{"DB_PASS": "hunter2"},
			wantServer:  "server",
			wantCommand: "echo hi",
		},
		{
			name:        "quoted --env value is unwrapped",
			args:        []string{"--env=\"DB_PASS=quoted\"", "server", "ls"},
			wantEnv:     map[string]string{"DB_PASS": "quoted"},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			name:        "value ending in a quote is not corrupted",
			args:        []string{"--env=PW=trailing\"", "server", "ls"},
			wantEnv:     map[string]string{"PW": "trailing\""},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			// Quotes wrap only the value, not the whole token—no matched pair to strip.
			name:        "inner-quoted value passes through verbatim",
			args:        []string{"--env=KEY=\"VAL\"", "server", "ls"},
			wantEnv:     map[string]string{"KEY": "\"VAL\""},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			name:        "--env=KEY for an unset variable warns and skips",
			args:        []string{"--env=DEFINITELY_UNSET", "server", "ls"},
			absentEnv:   []string{"DEFINITELY_UNSET"},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			name:        "multiple --env flags accumulate",
			args:        []string{"--env=A=1", "--env=B=2", "server", "ls"},
			wantEnv:     map[string]string{"A": "1", "B": "2"},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			name:        "--env=KEY= sets an empty value",
			args:        []string{"--env=EMPTY=", "server", "ls"},
			wantEnv:     map[string]string{"EMPTY": ""},
			wantServer:  "server",
			wantCommand: "ls",
		},
		{
			name:        "--env after server is a command arg, not a flag",
			args:        []string{"server", "--env=X=1"},
			absentEnv:   []string{"X"},
			wantServer:  "server",
			wantCommand: "--env=X=1",
		},
		{
			name:    "bare --env with no value is an error",
			args:    []string{"--env", "server", "ls"},
			wantErr: true,
		},
		{
			name:    "--env= with empty key is an error",
			args:    []string{"--env=", "server", "ls"},
			wantErr: true,
		},
		{
			name:    "--env-file is an unknown flag, not a malformed --env",
			args:    []string{"--env-file=secrets", "server", "ls"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)
			if tt.wantErr {
				assert.NotEmpty(t, result.Err)
				assert.Empty(t, result.Server)
				return
			}
			assert.Empty(t, result.Err)
			for k, v := range tt.wantEnv {
				assert.Equal(t, v, result.Env[k], "Env[%s]", k)
			}
			for _, k := range tt.absentEnv {
				_, ok := result.Env[k]
				assert.False(t, ok, "Env[%s] should be absent", k)
			}
			assert.Equal(t, tt.wantServer, result.Server, "Server")
			assert.Equal(t, tt.wantCommand, result.Command, "Command")
		})
	}
}

func TestParseRemoteExecArgs_EnvErrorHidesSecret(t *testing.T) {
	t.Parallel()
	result := ParseRemoteExecArgs([]string{"--env=\"=hunter2\"", "server", "ls"})
	assert.NotEmpty(t, result.Err)
	assert.NotContains(t, result.Err, "hunter2", "malformed --env error must not echo the value")
}

func TestParseRemoteExecArgs_EnvPrefixIsExact(t *testing.T) {
	t.Parallel()
	// --env-file must not be swallowed by --env matching; it is an unknown flag.
	result := ParseRemoteExecArgs([]string{"--env-file=secrets", "server", "ls"})
	assert.Contains(t, result.Err, "unknown flag")
}

func TestParseRemoteExecArgs_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		expectedErr string
	}{
		{
			name:        "unknown short flag -x",
			args:        []string{"-x", "server", "ls"},
			expectedErr: "unknown flag: -x",
		},
		{
			name:        "unknown long flag --unknown",
			args:        []string{"--unknown", "server", "ls"},
			expectedErr: "unknown flag: --unknown",
		},
		{
			name:        "typo -U before server (not after --)",
			args:        []string{"-U", "root", "server", "ls"},
			expectedErr: "unknown flag: -U",
		},
		{
			name:        "-u as last arg with no value",
			args:        []string{"-u"},
			expectedErr: "flag needs an argument: -u",
		},
		{
			name:        "-g as last arg with no value",
			args:        []string{"-g"},
			expectedErr: "flag needs an argument: -g",
		},
		{
			name:        "--username as last arg with no value",
			args:        []string{"--username"},
			expectedErr: "flag needs an argument: --username",
		},
		{
			name:        "--groupname as last arg with no value",
			args:        []string{"--groupname"},
			expectedErr: "flag needs an argument: --groupname",
		},
		{
			name:        "--detach=VALUE equals-form is rejected",
			args:        []string{"--detach=true", "server", "apt", "upgrade"},
			expectedErr: "--detach does not accept a value; use --detach alone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)
			assert.Equal(t, tt.expectedErr, result.Err)
			assert.Empty(t, result.Server)
		})
	}
}

// The --purpose flag (ADR 0052). The ceiling is counted in runes: the server
// validates with DRF's CharField(max_length=...), which counts characters, so a
// byte count would refuse a Korean purpose at roughly a third of the limit.
func TestParseRemoteExecArgsPurpose(t *testing.T) {
	t.Parallel()
	koreanAtCeiling := make([]rune, PurposeMaxLength)
	for i := range koreanAtCeiling {
		koreanAtCeiling[i] = '한'
	}
	overCeiling := make([]rune, PurposeMaxLength+1)
	for i := range overCeiling {
		overCeiling[i] = 'x'
	}

	for _, tc := range []struct {
		name        string
		args        []string
		wantErr     string
		wantPurpose string
	}{
		{
			name:        "separate value",
			args:        []string{"--purpose", "clock skew", "prod", "uptime"},
			wantPurpose: "clock skew",
		},
		{
			name:        "attached value",
			args:        []string{"--purpose=clock skew", "prod", "uptime"},
			wantPurpose: "clock skew",
		},
		{
			name:        "a 2000-character Korean purpose is within the ceiling",
			args:        []string{"--purpose", string(koreanAtCeiling), "prod", "uptime"},
			wantPurpose: string(koreanAtCeiling),
		},
		{
			name:    "blank is a usage error, not a 400 that spends the demand",
			args:    []string{"--purpose", "   ", "prod", "uptime"},
			wantErr: "--purpose requires a value",
		},
		{
			name:    "over the ceiling",
			args:    []string{"--purpose", string(overCeiling), "prod", "uptime"},
			wantErr: "limited to 2000 characters",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed := ParseRemoteExecArgs(tc.args)

			if tc.wantErr != "" {
				assert.Contains(t, parsed.Err, tc.wantErr)
				return
			}
			assert.Empty(t, parsed.Err)
			assert.Equal(t, tc.wantPurpose, parsed.Purpose)
		})
	}
}
