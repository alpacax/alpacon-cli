package ftp

import (
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestIsRemotePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "Local file path",
			path:     "./test.txt",
			expected: false,
		},
		{
			name:     "Local absolute path",
			path:     "/home/user/test.txt",
			expected: false,
		},
		{
			name:     "Simple hostname only",
			path:     "prod-docker",
			expected: false,
		},
		{
			name:     "User and hostname only",
			path:     "root@prod-docker",
			expected: false,
		},
		{
			name:     "Remote path with hostname",
			path:     "prod-docker:~/",
			expected: true,
		},
		{
			name:     "Remote path with user and hostname",
			path:     "root@prod-docker:/var/log/syslog",
			expected: true,
		},
		{
			name:     "Complex remote path",
			path:     "admin@prod-docker:~/eunyoung/test.txt",
			expected: true,
		},
		{
			name:     "Empty remote path",
			path:     "server:",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRemotePath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "Local file path",
			path:     "./test.txt",
			expected: true,
		},
		{
			name:     "Local absolute path",
			path:     "/home/user/test.txt",
			expected: true,
		},
		{
			name:     "Simple hostname only",
			path:     "prod-docker",
			expected: true,
		},
		{
			name:     "Remote path with hostname",
			path:     "prod-docker:~/",
			expected: false,
		},
		{
			name:     "Remote path with user and hostname",
			path:     "root@prod-docker:/var/log/syslog",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalPaths(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected bool
	}{
		{
			name:     "All local paths",
			paths:    []string{"./test.txt", "/home/user/file.txt"},
			expected: true,
		},
		{
			name:     "Mixed local and remote paths",
			paths:    []string{"./test.txt", "server:~/file.txt"},
			expected: false,
		},
		{
			name:     "All remote paths",
			paths:    []string{"server1:~/file1.txt", "server2:~/file2.txt"},
			expected: false,
		},
		{
			name:     "Single local path",
			paths:    []string{"./test.txt"},
			expected: true,
		},
		{
			name:     "Single remote path",
			paths:    []string{"server:~/test.txt"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalPaths(tt.paths)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test the SSH parsing logic used in the cp command
func TestCpCommandSSHParsing(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectedArgs []string
		expectedUser string
		description  string
	}{
		{
			name:         "Simple local to remote copy",
			args:         []string{"test.txt", "prod-docker:~/"},
			expectedArgs: []string{"test.txt", "prod-docker:~/"},
			expectedUser: "",
			description:  "alpacon cp test.txt prod-docker:~/",
		},
		{
			name:         "Local to remote with path",
			args:         []string{"test.txt", "prod-docker:~/eunyoung/"},
			expectedArgs: []string{"test.txt", "prod-docker:~/eunyoung/"},
			expectedUser: "",
			description:  "alpacon cp test.txt prod-docker:~/eunyoung/",
		},
		{
			name:         "Remote to local copy",
			args:         []string{"prod-docker:~/eunyoung/test.txt", "."},
			expectedArgs: []string{"prod-docker:~/eunyoung/test.txt", "."},
			expectedUser: "",
			description:  "alpacon cp prod-docker:~/eunyoung/test.txt .",
		},
		{
			name:         "Remote with user to local",
			args:         []string{"root@prod-docker:/var/log/syslog", "."},
			expectedArgs: []string{"prod-docker:/var/log/syslog", "."},
			expectedUser: "root",
			description:  "alpacon cp root@prod-docker:/var/log/syslog .",
		},
		{
			name:         "Local to remote with user",
			args:         []string{"test.txt", "admin@prod-docker:~/uploads/"},
			expectedArgs: []string{"test.txt", "prod-docker:~/uploads/"},
			expectedUser: "admin",
			description:  "alpacon cp test.txt admin@prod-docker:~/uploads/",
		},
		{
			name:         "Multiple local files to remote with user",
			args:         []string{"file1.txt", "file2.txt", "deploy@web-server:/opt/app/"},
			expectedArgs: []string{"file1.txt", "file2.txt", "web-server:/opt/app/"},
			expectedUser: "deploy",
			description:  "alpacon cp file1.txt file2.txt deploy@web-server:/opt/app/",
		},
		{
			name:         "User in hostname only without colon — not parsed as SSH",
			args:         []string{"test.txt", "root@prod-docker"},
			expectedArgs: []string{"test.txt", "root@prod-docker"},
			expectedUser: "",
			description:  "alpacon cp test.txt root@prod-docker (no colon, treated as literal arg)",
		},
		{
			name:         "Local file with @ in name — not parsed as SSH",
			args:         []string{"report@2026.txt", "prod-docker:~/uploads/"},
			expectedArgs: []string{"report@2026.txt", "prod-docker:~/uploads/"},
			expectedUser: "",
			description:  "alpacon cp report@2026.txt prod-docker:~/uploads/ (@ in local filename)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the parsing logic from the cp command
			args := make([]string, len(tt.args))
			copy(args, tt.args)
			username := ""

			// Apply the same parsing logic as in cp.go
			for i, arg := range args {
				if strings.Contains(arg, "@") && strings.Contains(arg, ":") {
					sshTarget := utils.ParseSSHTarget(arg)
					if username == "" && sshTarget.User != "" {
						username = sshTarget.User
					}
					if sshTarget.Path != "" {
						args[i] = sshTarget.Host + ":" + sshTarget.Path
					} else {
						args[i] = sshTarget.Host
					}
				}
			}

			assert.Equal(t, tt.expectedArgs, args, "Arguments should match expected after parsing")
			assert.Equal(t, tt.expectedUser, username, "Username should match expected")
		})
	}
}

// Test scenarios covering the required patterns from the issue
func TestRequiredCpPatterns(t *testing.T) {
	patterns := []struct {
		description    string
		command        string
		args           []string
		shouldBeRemote bool
		shouldBeLocal  bool
	}{
		{
			description:    "alpacon cp test.txt prod-docker:~/",
			command:        "cp",
			args:           []string{"test.txt", "prod-docker:~/"},
			shouldBeRemote: true, // destination is remote
			shouldBeLocal:  false,
		},
		{
			description:    "alpacon cp test.txt prod-docker:~/eunyoung/",
			command:        "cp",
			args:           []string{"test.txt", "prod-docker:~/eunyoung/"},
			shouldBeRemote: true, // destination is remote
			shouldBeLocal:  false,
		},
		{
			description:    "alpacon cp prod-docker:~/eunyoung/test.txt .",
			command:        "cp",
			args:           []string{"prod-docker:~/eunyoung/test.txt", "."},
			shouldBeRemote: false, // destination is local
			shouldBeLocal:  true,
		},
		{
			description:    "alpacon cp root@prod-docker:/var/log/syslog .",
			command:        "cp",
			args:           []string{"root@prod-docker:/var/log/syslog", "."},
			shouldBeRemote: false, // destination is local
			shouldBeLocal:  true,
		},
	}

	for _, pattern := range patterns {
		t.Run(pattern.description, func(t *testing.T) {
			if len(pattern.args) >= 2 {
				sources := pattern.args[:len(pattern.args)-1]
				dest := pattern.args[len(pattern.args)-1]

				// Test the logic that determines upload vs download
				isUpload := isLocalPaths(sources) && isRemotePath(dest)
				isDownload := isRemotePath(sources[0]) && isLocalPath(dest)

				if pattern.shouldBeRemote {
					assert.True(t, isUpload, "Should be an upload operation (local to remote)")
					assert.False(t, isDownload, "Should not be a download operation")
				} else if pattern.shouldBeLocal {
					assert.True(t, isDownload, "Should be a download operation (remote to local)")
					assert.False(t, isUpload, "Should not be an upload operation")
				}
			}
		})
	}
}
