package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExecCommandParsing validates backward compatibility with the original
// parsing behavior using the new ParseRemoteExecArgs implementation.
func TestExecCommandParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedUsername  string
		expectedGroupname string
		expectedServer    string
		expectedCommand   string
	}{
		{
			name:            "Simple command execution",
			args:            []string{"prod-docker", "docker", "ps"},
			expectedServer:  "prod-docker",
			expectedCommand: "docker ps",
		},
		{
			name:            "User@host syntax",
			args:            []string{"root@prod-docker", "docker", "ps"},
			expectedUsername: "root",
			expectedServer:  "prod-docker",
			expectedCommand: "docker ps",
		},
		{
			name:            "Complex command with user",
			args:            []string{"admin@web-server", "ls", "-la", "/var/log"},
			expectedUsername: "admin",
			expectedServer:  "web-server",
			expectedCommand: "ls -la /var/log",
		},
		{
			name:            "Single word command",
			args:            []string{"server", "uptime"},
			expectedServer:  "server",
			expectedCommand: "uptime",
		},
		{
			name:            "Complex hostname with user",
			args:            []string{"deploy@web-server-01.example.com", "systemctl", "status", "nginx"},
			expectedUsername: "deploy",
			expectedServer:  "web-server-01.example.com",
			expectedCommand: "systemctl status nginx",
		},
		{
			name:            "Command with pipes and special characters",
			args:            []string{"root@server", "ps", "aux", "|", "grep", "nginx"},
			expectedUsername: "root",
			expectedServer:  "server",
			expectedCommand: "ps aux | grep nginx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)

			assert.Equal(t, tt.expectedUsername, result.Username, "Username should match")
			assert.Equal(t, tt.expectedGroupname, result.Groupname, "Groupname should match")
			assert.Equal(t, tt.expectedServer, result.Server, "Server name should match")
			assert.Equal(t, tt.expectedCommand, result.Command, "Command should match")
		})
	}
}

func TestExecCommandParsingWithFlags(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedUsername  string
		expectedGroupname string
		expectedServer    string
		expectedCommand   string
	}{
		{
			name:            "Username flag overrides user@host",
			args:            []string{"-u", "override", "root@prod-docker", "docker", "ps"},
			expectedUsername: "override",
			expectedServer:  "prod-docker",
			expectedCommand: "docker ps",
		},
		{
			name:              "Groupname flag with user@host",
			args:              []string{"-g", "docker", "admin@server", "ls"},
			expectedUsername:  "admin",
			expectedGroupname: "docker",
			expectedServer:    "server",
			expectedCommand:   "ls",
		},
		{
			name:              "Both flags with user@host",
			args:              []string{"-u", "flag-user", "-g", "flag-group", "user@server", "uptime"},
			expectedUsername:  "flag-user",
			expectedGroupname: "flag-group",
			expectedServer:    "server",
			expectedCommand:   "uptime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRemoteExecArgs(tt.args)

			assert.Equal(t, tt.expectedUsername, result.Username, "Username should match")
			assert.Equal(t, tt.expectedGroupname, result.Groupname, "Groupname should match")
			assert.Equal(t, tt.expectedServer, result.Server, "Server name should match")
			assert.Equal(t, tt.expectedCommand, result.Command, "Command should match")
		})
	}
}

// TestRequiredExecPattern validates the exact pattern from the issue description.
func TestRequiredExecPattern(t *testing.T) {
	args := []string{"root@prod-docker", "docker", "ps"}
	result := ParseRemoteExecArgs(args)

	assert.Equal(t, "root", result.Username, "Username should be extracted from user@host")
	assert.Equal(t, "prod-docker", result.Server, "Server name should be extracted correctly")
	assert.Equal(t, "docker ps", result.Command, "Command should be preserved")
}
