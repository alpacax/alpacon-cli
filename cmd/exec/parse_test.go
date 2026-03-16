package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRemoteExecArgs(t *testing.T) {
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
			name: "empty args",
			args: []string{},
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
				Command: "echo hello world",
			},
		},
		{
			name: "single-quoted arg preserved",
			args: []string{"server", "bash", "-c", "echo 'hello world'"},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: "bash -c echo 'hello world'",
			},
		},
		{
			name: "double-quoted arg preserved",
			args: []string{"server", "bash", "-c", `echo "hello world"`},
			expected: RemoteExecArgs{
				Server:  "server",
				Command: `bash -c echo "hello world"`,
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
				Command:  "docker exec -it postgres psql -U myproject -d myproject -c SELECT 1;",
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
				Command: "awk {print $1} /var/log/access.log",
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
				Command: "curl -s -H Authorization: Bearer token123 http://localhost:8080/api/health",
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
			name: "-u as last arg with no value",
			args: []string{"-u"},
			expected: RemoteExecArgs{
				Username: "",
				Server:   "",
				Command:  "",
			},
		},
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
			name: "-g with only value, no server",
			args: []string{"-g", "docker"},
			expected: RemoteExecArgs{
				Groupname: "docker",
				Server:    "",
				Command:   "",
			},
		},
		{
			name: "only -- with nothing else",
			args: []string{"--"},
			expected: RemoteExecArgs{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)

			assert.Equal(t, tt.expected.Username, result.Username, "Username")
			assert.Equal(t, tt.expected.Groupname, result.Groupname, "Groupname")
			assert.Equal(t, tt.expected.Server, result.Server, "Server")
			assert.Equal(t, tt.expected.Command, result.Command, "Command")
		})
	}
}

func TestParseRemoteExecArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			result := ParseRemoteExecArgs([]string{flag, "server", "ls"})
			assert.Empty(t, result.Server, "help flag should return empty result")
		})
	}
}
