package websh

import (
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestCommandParsing(t *testing.T) {
	tests := []struct {
		testName          string
		args              []string
		expectUsername    string
		expectGroupname   string
		expectServerName  string
		expectEnv         map[string]string
		expectCommandArgs []string
		expectShare       bool
		expectJoin        bool
		expectReadOnly    bool
		expectUrl         string
		expectPassword    string
	}{
		{
			testName:          "RootAccessToServer",
			args:              []string{"-r", "prod-server", "df", "-h"},
			expectUsername:    "root",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "prod-server",
			expectCommandArgs: []string{"df", "-h"},
		},
		{
			testName:          "ExecuteUpdateAsAdminSysadmin",
			args:              []string{"-u", "admin", "-g", "sysadmin", "update-server", "sudo", "apt-get", "update"},
			expectUsername:    "admin",
			expectGroupname:   "sysadmin",
			expectEnv:         map[string]string{},
			expectServerName:  "update-server",
			expectCommandArgs: []string{"sudo", "apt-get", "update"},
		},
		{
			testName:          "DockerComposeDeploymentWithFlags",
			args:              []string{"deploy-server", "docker-compose", "-f", "/home/admin/deploy/docker-compose.yml", "up", "-d"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "deploy-server",
			expectCommandArgs: []string{"docker-compose", "-f", "/home/admin/deploy/docker-compose.yml", "up", "-d"},
		},
		{
			testName:          "VerboseListInFileServer",
			args:              []string{"file-server", "ls", "-l", "/var/www"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "file-server",
			expectCommandArgs: []string{"ls", "-l", "/var/www"},
		},
		{
			testName:          "MisplacedFlagOrderWithRoot",
			args:              []string{"-r", "df", "-h"},
			expectUsername:    "root",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "df",
			expectCommandArgs: []string(nil),
		},
		{
			testName:          "UnrecognizedFlagWithEchoCommand",
			args:              []string{"-x", "unknown-server", "echo", "Hello World"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "-x",
			expectCommandArgs: []string{"unknown-server", "echo", "Hello World"},
		},
		{
			testName:          "AdminSysadminAccessToMultiFlagServer",
			args:              []string{"--username=admin", "--groupname=sysadmin", "multi-flag-server", "uptime"},
			expectUsername:    "admin",
			expectGroupname:   "sysadmin",
			expectEnv:         map[string]string{},
			expectServerName:  "multi-flag-server",
			expectCommandArgs: []string{"uptime"},
		},
		{
			testName:          "CommandLineArgsResembleFlags",
			args:              []string{"--username", "admin", "server-name", "--fake-flag", "value"},
			expectUsername:    "admin",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"--fake-flag", "value"},
		},
		{
			testName:          "SysadminGroupWithMixedSyntax",
			args:              []string{"-g=sysadmin", "server-name", "echo", "hello world"},
			expectUsername:    "",
			expectGroupname:   "sysadmin",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"echo", "hello world"},
		},
		{
			testName:          "HelpRequestedViaCombinedFlags",
			args:              []string{"-rh"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "-rh",
			expectCommandArgs: nil,
		},
		{
			testName:          "InvalidUsageDetected",
			args:              []string{"-u", "user", "-x", "unknown-flag", "server-name", "cmd"},
			expectUsername:    "user",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "-x",
			expectCommandArgs: []string{"unknown-flag", "server-name", "cmd"},
		},
		{
			testName:          "ValidFlagsFollowedByInvalidFlag",
			args:              []string{"-u", "user", "-g", "group", "-x", "server-name", "cmd"},
			expectUsername:    "user",
			expectGroupname:   "group",
			expectEnv:         map[string]string{},
			expectServerName:  "-x",
			expectCommandArgs: []string{"server-name", "cmd"},
		},
		{
			testName:          "FlagsIntermixedWithCommandArgs",
			args:              []string{"server-name", "-u", "user", "cmd", "-g", "group"},
			expectUsername:    "user",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd", "-g", "group"},
		},
		{
			testName:          "FlagsAndCommandArgsIntertwined",
			args:              []string{"server-name", "-u", "user", "cmd", "-g", "group"},
			expectUsername:    "user",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd", "-g", "group"},
		},
		{
			testName:          "ShareSessionWithFlags",
			args:              []string{"test-server", "--share"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "test-server",
			expectCommandArgs: nil,
			expectShare:       true,
			expectJoin:        false,
			expectReadOnly:    false,
			expectUrl:         "",
			expectPassword:    "",
		},
		{
			testName:          "JoinSharedSession",
			args:              []string{"join", "--url", "http://localhost:3000/websh/join?session=abcd", "--password", "1234"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "join",
			expectCommandArgs: nil,
			expectShare:       false,
			expectJoin:        true,
			expectReadOnly:    false,
			expectUrl:         "http://localhost:3000/websh/join?session=abcd",
			expectPassword:    "1234",
		},
		{
			testName:          "ReadOnlySharedSession",
			args:              []string{"test-server", "--share", "--read-only"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "test-server",
			expectCommandArgs: nil,
			expectShare:       true,
			expectJoin:        false,
			expectReadOnly:    true,
			expectUrl:         "",
			expectPassword:    "",
		},
		{
			testName:          "ReadOnlySharedSession2",
			args:              []string{"test-server", "--share", "--read-only=True"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "test-server",
			expectCommandArgs: nil,
			expectShare:       true,
			expectJoin:        false,
			expectReadOnly:    true,
			expectUrl:         "",
			expectPassword:    "",
		},
		{
			testName:          "InvalidFlagCombination",
			args:              []string{"--share", "join", "--url", "http://localhost:3000/websh/join?session=abcd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "join",
			expectCommandArgs: nil,
			expectShare:       true,
			expectJoin:        true,
			expectReadOnly:    false,
			expectUrl:         "http://localhost:3000/websh/join?session=abcd",
			expectPassword:    "",
		},
		{
			testName:          "SingleEnvVariable",
			args:              []string{"--env=KEY1=value1", "server-name", "cmd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{"KEY1": "value1"},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd"},
		},
		{
			testName:          "MultipleEnvVariables",
			args:              []string{"--env=KEY1=value1", "--env=KEY2=value2", "server-name", "cmd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{"KEY1": "value1", "KEY2": "value2"},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd"},
		},
		{
			testName:          "EnvKeyWithoutValue",
			args:              []string{"--env=KEY", "server-name", "cmd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd"},
		},
		{
			testName:          "ShellCommand",
			args:              []string{"server-name", "ls; cat /etc/passwd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"ls; cat /etc/passwd"},
		},
		{
			testName:          "UserAtHostSyntax",
			args:              []string{"root@prod-docker"},
			expectUsername:    "root",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "prod-docker",
			expectCommandArgs: nil,
		},
		{
			testName:          "UserAtHostWithCommand",
			args:              []string{"admin@web-server", "docker", "ps"},
			expectUsername:    "admin",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "web-server",
			expectCommandArgs: []string{"docker", "ps"},
		},
		{
			testName:          "UserFlagOverridesUserAtHost",
			args:              []string{"-u", "override", "root@prod-docker", "ls"},
			expectUsername:    "override",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "prod-docker",
			expectCommandArgs: []string{"ls"},
		},
		{
			testName:          "ComplexHostnameWithUser",
			args:              []string{"deploy@web-server-01.example.com", "uptime"},
			expectUsername:    "deploy",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "web-server-01.example.com",
			expectCommandArgs: []string{"uptime"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.testName, func(t *testing.T) {
			username, groupname, serverName, commandArgs, share, join, readOnly, url, password, env := executeTestCommand(tc.args)

			assert.Equal(t, tc.expectUsername, username, "Mismatch in username")
			assert.Equal(t, tc.expectGroupname, groupname, "Mismatch in groupname")
			assert.Equal(t, tc.expectServerName, serverName, "Mismatch in server name")
			assert.Equal(t, tc.expectCommandArgs, commandArgs, "Mismatch in command arguments")
			assert.Equal(t, tc.expectShare, share, "Mismatch in share flag")
			assert.Equal(t, tc.expectJoin, join, "Mismatch in join functionality")
			assert.Equal(t, tc.expectReadOnly, readOnly, "Mismatch in read-only flag")
			assert.Equal(t, tc.expectUrl, url, "Mismatch in URL for joining")
			assert.Equal(t, tc.expectPassword, password, "Mismatch in password for joining")
			assert.Equal(t, tc.expectEnv, env, "Mismatch in env")
		})
	}
}

func executeTestCommand(args []string) (string, string, string, []string, bool, bool, bool, string, string, map[string]string) {
	var (
		share, join, readOnly                          bool
		username, groupname, serverName, url, password string
		commandArgs                                    []string
	)

	env := make(map[string]string)

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-r" || args[i] == "--root":
			username = "root"
		case args[i] == "-s" || args[i] == "--share":
			share = true
		case args[i] == "-h" || args[i] == "--help":
			return username, groupname, serverName, commandArgs, share, join, readOnly, url, password, env
		case strings.HasPrefix(args[i], "-u") || strings.HasPrefix(args[i], "--username"):
			username, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "-g") || strings.HasPrefix(args[i], "--groupname"):
			groupname, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "--url"):
			url, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "-p") || strings.HasPrefix(args[i], "--password"):
			password, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "--env"):
			i = extractEnvValue(args, i, env)
		case strings.HasPrefix(args[i], "--read-only"):
			var value string
			value, i = extractValue(args, i)
			if value == "" || strings.TrimSpace(strings.ToLower(value)) == "true" {
				readOnly = true
			} else if strings.TrimSpace(strings.ToLower(value)) == "false" {
				readOnly = false
			} else {
				utils.CliErrorWithExit("The 'read only' value must be either 'true' or 'false'.")
			}
		default:
			if serverName == "" {
				serverName = args[i]
			} else {
				commandArgs = append(commandArgs, args[i:]...)
				i = len(args)
			}
		}
	}

	if serverName == "join" {
		join = true
	}

	// Parse SSH-like syntax for user@host (same logic as in the main command)
	if strings.Contains(serverName, "@") && !strings.Contains(serverName, ":") {
		sshTarget := utils.ParseSSHTarget(serverName)
		if username == "" && sshTarget.User != "" {
			username = sshTarget.User
		}
		serverName = sshTarget.Host
	}

	return username, groupname, serverName, commandArgs, share, join, readOnly, url, password, env
}
