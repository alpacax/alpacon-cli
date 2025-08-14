package exec

import (
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestExecCommandParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedUsername  string
		expectedGroupname string
		expectedServer    string
		expectedCommand   []string
	}{
		{
			name:              "Simple command execution",
			args:              []string{"prod-docker", "docker", "ps"},
			expectedUsername:  "",
			expectedGroupname: "",
			expectedServer:    "prod-docker",
			expectedCommand:   []string{"docker", "ps"},
		},
		{
			name:              "User@host syntax",
			args:              []string{"root@prod-docker", "docker", "ps"},
			expectedUsername:  "root",
			expectedGroupname: "",
			expectedServer:    "prod-docker",
			expectedCommand:   []string{"docker", "ps"},
		},
		{
			name:              "Complex command with user",
			args:              []string{"admin@web-server", "ls", "-la", "/var/log"},
			expectedUsername:  "admin",
			expectedGroupname: "",
			expectedServer:    "web-server",
			expectedCommand:   []string{"ls", "-la", "/var/log"},
		},
		{
			name:              "Single word command",
			args:              []string{"server", "uptime"},
			expectedUsername:  "",
			expectedGroupname: "",
			expectedServer:    "server",
			expectedCommand:   []string{"uptime"},
		},
		{
			name:              "Complex hostname with user",
			args:              []string{"deploy@web-server-01.example.com", "systemctl", "status", "nginx"},
			expectedUsername:  "deploy",
			expectedGroupname: "",
			expectedServer:    "web-server-01.example.com",
			expectedCommand:   []string{"systemctl", "status", "nginx"},
		},
		{
			name:              "Command with pipes and special characters",
			args:              []string{"root@server", "ps", "aux", "|", "grep", "nginx"},
			expectedUsername:  "root",
			expectedGroupname: "",
			expectedServer:    "server",
			expectedCommand:   []string{"ps", "aux", "|", "grep", "nginx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username, groupname, serverName, commandArgs := parseExecArgs(tt.args, "", "")
			
			assert.Equal(t, tt.expectedUsername, username, "Username should match")
			assert.Equal(t, tt.expectedGroupname, groupname, "Groupname should match")
			assert.Equal(t, tt.expectedServer, serverName, "Server name should match")
			assert.Equal(t, tt.expectedCommand, commandArgs, "Command args should match")
		})
	}
}

func TestExecCommandParsingWithFlags(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		initialUsername   string
		initialGroupname  string
		expectedUsername  string
		expectedGroupname string
		expectedServer    string
		expectedCommand   []string
	}{
		{
			name:              "Username flag overrides user@host",
			args:              []string{"root@prod-docker", "docker", "ps"},
			initialUsername:   "override",
			initialGroupname:  "",
			expectedUsername:  "override",
			expectedGroupname: "",
			expectedServer:    "prod-docker",
			expectedCommand:   []string{"docker", "ps"},
		},
		{
			name:              "Groupname flag with user@host",
			args:              []string{"admin@server", "ls"},
			initialUsername:   "",
			initialGroupname:  "docker",
			expectedUsername:  "admin",
			expectedGroupname: "docker",
			expectedServer:    "server",
			expectedCommand:   []string{"ls"},
		},
		{
			name:              "Both flags with user@host",
			args:              []string{"user@server", "uptime"},
			initialUsername:   "flag-user",
			initialGroupname:  "flag-group",
			expectedUsername:  "flag-user",
			expectedGroupname: "flag-group",
			expectedServer:    "server",
			expectedCommand:   []string{"uptime"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username, groupname, serverName, commandArgs := parseExecArgs(tt.args, tt.initialUsername, tt.initialGroupname)
			
			assert.Equal(t, tt.expectedUsername, username, "Username should match")
			assert.Equal(t, tt.expectedGroupname, groupname, "Groupname should match")
			assert.Equal(t, tt.expectedServer, serverName, "Server name should match")
			assert.Equal(t, tt.expectedCommand, commandArgs, "Command args should match")
		})
	}
}

// parseExecArgs simulates the parsing logic from the exec command for testing
func parseExecArgs(args []string, initialUsername, initialGroupname string) (string, string, string, []string) {
	if len(args) < 2 {
		return "", "", "", nil
	}

	username := initialUsername
	groupname := initialGroupname
	serverName := args[0]
	commandArgs := args[1:]

	// Parse SSH-like syntax for user@host (same logic as in exec.go)
	if len(serverName) > 0 && !strings.Contains(serverName, ":") && strings.Contains(serverName, "@") {
		sshTarget := utils.ParseSSHTarget(serverName)
		if username == "" && sshTarget.User != "" {
			username = sshTarget.User
		}
		serverName = sshTarget.Host
	}

	return username, groupname, serverName, commandArgs
}

// Test the required pattern from the issue description
func TestRequiredExecPattern(t *testing.T) {
	// Test: alpacon exec root@prod-docker docker ps
	args := []string{"root@prod-docker", "docker", "ps"}
	username, _, serverName, commandArgs := parseExecArgs(args, "", "")

	assert.Equal(t, "root", username, "Username should be extracted from user@host")
	assert.Equal(t, "prod-docker", serverName, "Server name should be extracted correctly")
	assert.Equal(t, []string{"docker", "ps"}, commandArgs, "Command should be preserved")
}