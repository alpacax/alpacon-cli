package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSSHTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected SSHTarget
	}{
		{
			name:  "Simple hostname",
			input: "prod-docker",
			expected: SSHTarget{
				User: "",
				Host: "prod-docker",
				Path: "",
			},
		},
		{
			name:  "User and hostname",
			input: "root@prod-docker",
			expected: SSHTarget{
				User: "root",
				Host: "prod-docker",
				Path: "",
			},
		},
		{
			name:  "Hostname and path",
			input: "prod-docker:~/",
			expected: SSHTarget{
				User: "",
				Host: "prod-docker",
				Path: "~/",
			},
		},
		{
			name:  "Hostname and complex path",
			input: "prod-docker:~/alice/",
			expected: SSHTarget{
				User: "",
				Host: "prod-docker",
				Path: "~/alice/",
			},
		},
		{
			name:  "User, hostname and path",
			input: "root@prod-docker:/var/log/syslog",
			expected: SSHTarget{
				User: "root",
				Host: "prod-docker",
				Path: "/var/log/syslog",
			},
		},
		{
			name:  "Complex user with hostname and path",
			input: "admin@prod-docker:~/alice/test.txt",
			expected: SSHTarget{
				User: "admin",
				Host: "prod-docker",
				Path: "~/alice/test.txt",
			},
		},
		{
			name:  "Empty path",
			input: "server:",
			expected: SSHTarget{
				User: "",
				Host: "server",
				Path: "",
			},
		},
		{
			name:  "User with empty path",
			input: "user@server:",
			expected: SSHTarget{
				User: "user",
				Host: "server",
				Path: "",
			},
		},
		{
			name:  "Complex hostname",
			input: "web-server-01.example.com",
			expected: SSHTarget{
				User: "",
				Host: "web-server-01.example.com",
				Path: "",
			},
		},
		{
			name:  "User with complex hostname and absolute path",
			input: "deploy@web-server-01.example.com:/opt/app/config",
			expected: SSHTarget{
				User: "deploy",
				Host: "web-server-01.example.com",
				Path: "/opt/app/config",
			},
		},
		{
			name:  "Remote path contains at sign",
			input: "prod-docker:/tmp/alice@example.com",
			expected: SSHTarget{
				User: "",
				Host: "prod-docker",
				Path: "/tmp/alice@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseSSHTarget(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRemoteTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Local file path",
			input:    "./test.txt",
			expected: false,
		},
		{
			name:     "Local absolute path",
			input:    "/home/user/test.txt",
			expected: false,
		},
		{
			name:     "Simple hostname only",
			input:    "prod-docker",
			expected: false,
		},
		{
			name:     "User and hostname only",
			input:    "root@prod-docker",
			expected: false,
		},
		{
			name:     "Remote path with hostname",
			input:    "prod-docker:~/",
			expected: true,
		},
		{
			name:     "Remote path with user and hostname",
			input:    "root@prod-docker:/var/log/syslog",
			expected: true,
		},
		{
			name:     "Complex remote path",
			input:    "admin@prod-docker:~/alice/test.txt",
			expected: true,
		},
		{
			name:     "Empty remote path",
			input:    "server:",
			expected: true,
		},
		{
			name:     "User with empty remote path",
			input:    "user@server:",
			expected: true,
		},
		{
			name:     "Remote path contains at sign",
			input:    "prod-docker:/tmp/alice@example.com",
			expected: true,
		},
		{
			name:     "Windows drive letter with backslash",
			input:    `C:\Users\alice\notes.txt`,
			expected: false,
		},
		{
			name:     "Windows drive root",
			input:    `D:\`,
			expected: false,
		},
		{
			name:     "Single-letter server with a relative path",
			input:    "c:notes.txt",
			expected: true,
		},
		{
			name:     "Single-letter server with an absolute path and a user",
			input:    "alice@h:/tmp/file",
			expected: true,
		},
		{
			name:     "Windows extended-length path",
			input:    `\\?\C:\Users\alice\notes.txt`,
			expected: false,
		},
		{
			name:     "Windows device path",
			input:    `\\.\C:\notes.txt`,
			expected: false,
		},
		{
			name:     "Dot-slash path holding a colon",
			input:    "./a:b.txt",
			expected: false,
		},
		{
			name:     "Dot-dot-slash path holding a colon",
			input:    "../a:b.txt",
			expected: false,
		},
		{
			name:     "Dot-dot-backslash path holding a colon",
			input:    `..\a:b.txt`,
			expected: false,
		},
		{
			name:     "Lowercase Windows drive letter",
			input:    `c:\Users\alice\notes.txt`,
			expected: false,
		},
		{
			name:     "User-prefixed single-letter server with a backslash path",
			input:    `alice@h:\notes.txt`,
			expected: true,
		},
		{
			name:     "Drive letter and colon alone",
			input:    "C:",
			expected: true,
		},
		{
			name:     "Dot-backslash path holding a colon",
			input:    `.\a:b.txt`,
			expected: false,
		},
		{
			name:     "Absolute path holding a colon",
			input:    "/tmp/a:b.txt",
			expected: false,
		},
		{
			name:     "Dot-slash path holding an at sign and a colon",
			input:    "./a@b:c.txt",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRemoteTarget(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Local file path",
			input:    "./test.txt",
			expected: true,
		},
		{
			name:     "Local absolute path",
			input:    "/home/user/test.txt",
			expected: true,
		},
		{
			name:     "Simple hostname only",
			input:    "prod-docker",
			expected: true,
		},
		{
			name:     "User and hostname only",
			input:    "root@prod-docker",
			expected: true,
		},
		{
			name:     "Remote path with hostname",
			input:    "prod-docker:~/",
			expected: false,
		},
		{
			name:     "Remote path with user and hostname",
			input:    "root@prod-docker:/var/log/syslog",
			expected: false,
		},
		{
			name:     "Remote path contains at sign",
			input:    "prod-docker:/tmp/alice@example.com",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLocalTarget(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRemoteTargetByPlatform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		goos     string
		input    string
		expected bool
	}{
		{name: "Windows reads C colon slash as a local path", goos: "windows", input: "C:/Users/alice/notes.txt", expected: false},
		{name: "Linux reads C colon slash as server C", goos: "linux", input: "C:/Users/alice/notes.txt", expected: true},
		{name: "Darwin reads h colon slash as server h", goos: "darwin", input: "h:/x", expected: true},
		{name: "Windows still reads a two-letter server as remote", goos: "windows", input: "ab:/x", expected: true},
		{name: "Windows still reads a user-prefixed single-letter server as remote", goos: "windows", input: "alice@h:/x", expected: true},
		{name: "Backslash drive path is local on Linux", goos: "linux", input: `C:\dl\`, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isRemoteTargetOn(tt.goos, tt.input))
		})
	}
}
