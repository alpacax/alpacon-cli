package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSSHTarget(t *testing.T) {
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
			input: "prod-docker:~/eunyoung/",
			expected: SSHTarget{
				User: "",
				Host: "prod-docker",
				Path: "~/eunyoung/",
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
			input: "admin@prod-docker:~/eunyoung/test.txt",
			expected: SSHTarget{
				User: "admin",
				Host: "prod-docker",
				Path: "~/eunyoung/test.txt",
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
			input:    "admin@prod-docker:~/eunyoung/test.txt",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRemoteTarget(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalTarget(t *testing.T) {
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
