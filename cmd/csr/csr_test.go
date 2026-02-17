package csr

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestCsrCommandStructure(t *testing.T) {
	assert.Equal(t, "csr", CsrCmd.Use)
	assert.True(t, CsrCmd.HasSubCommands())
}

func TestCsrSubcommands(t *testing.T) {
	tests := []struct {
		name    string
		cmdName string
		aliases []string
	}{
		{
			name:    "create subcommand",
			cmdName: "create",
		},
		{
			name:    "ls subcommand",
			cmdName: "ls",
			aliases: []string{"list", "all"},
		},
		{
			name:    "approve subcommand",
			cmdName: "approve",
		},
		{
			name:    "deny subcommand",
			cmdName: "deny",
		},
		{
			name:    "delete subcommand",
			cmdName: "delete",
			aliases: []string{"rm"},
		},
		{
			name:    "describe subcommand",
			cmdName: "describe",
			aliases: []string{"desc"},
		},
		{
			name:    "download-crt subcommand",
			cmdName: "download-crt",
		},
		{
			name:    "retry subcommand",
			cmdName: "retry",
		},
	}

	subCmds := CsrCmd.Commands()
	subCmdNames := make(map[string]bool)
	for _, cmd := range subCmds {
		subCmdNames[cmd.Name()] = true
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, subCmdNames[tt.cmdName], "subcommand %q should be registered", tt.cmdName)

			cmd, _, err := CsrCmd.Find([]string{tt.cmdName})
			assert.NoError(t, err)
			assert.NotNil(t, cmd)

			if tt.aliases != nil {
				assert.Equal(t, tt.aliases, cmd.Aliases)
			}
		})
	}

	assert.Equal(t, 8, len(subCmds), "should have 8 subcommands")
}

func TestSubcommandAliases(t *testing.T) {
	tests := []struct {
		name      string
		alias     string
		expectCmd string
	}{
		{name: "ls via list", alias: "list", expectCmd: "ls"},
		{name: "ls via all", alias: "all", expectCmd: "ls"},
		{name: "delete via rm", alias: "rm", expectCmd: "delete"},
		{name: "describe via desc", alias: "desc", expectCmd: "describe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := CsrCmd.Find([]string{tt.alias})
			assert.NoError(t, err)
			assert.Equal(t, tt.expectCmd, cmd.Name())
		})
	}
}

func TestListSubcommandFlags(t *testing.T) {
	cmd, _, err := CsrCmd.Find([]string{"ls"})
	assert.NoError(t, err)

	statusFlag := cmd.Flags().Lookup("status")
	assert.NotNil(t, statusFlag, "--status flag should exist")
	assert.Equal(t, "s", statusFlag.Shorthand, "short flag should be -s")
}

func TestDownloadSubcommandFlags(t *testing.T) {
	cmd, _, err := CsrCmd.Find([]string{"download-crt"})
	assert.NoError(t, err)

	outFlag := cmd.Flags().Lookup("out")
	assert.NotNil(t, outFlag, "--out flag should exist")
	assert.Equal(t, "o", outFlag.Shorthand, "short flag should be -o")
	assert.Equal(t, "", outFlag.DefValue, "default value should be empty")
}

func TestEnsureSecureConnection_HTTPS(t *testing.T) {
	ac := &client.AlpaconClient{
		HTTPClient: &http.Client{},
		BaseURL:    "https://secure.example.com",
	}

	// HTTPS should pass without prompting or exiting
	assert.NotPanics(t, func() {
		EnsureSecureConnection(ac)
	})
}
