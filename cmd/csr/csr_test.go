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
			aliases: []string{"list"},
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

func TestCreateSubcommandFlags(t *testing.T) {
	cmd, _, err := CsrCmd.Find([]string{"create"})
	assert.NoError(t, err)

	tests := []struct {
		flagName  string
		shorthand string
		defValue  string
	}{
		{"domain", "d", ""},
		{"ip", "i", ""},
		{"key", "k", ""},
		{"out", "o", ""},
	}
	for _, tt := range tests {
		t.Run("flag --"+tt.flagName, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flagName)
			assert.NotNil(t, f, "--%s flag should exist", tt.flagName)
			assert.Equal(t, tt.shorthand, f.Shorthand)
			assert.Equal(t, tt.defValue, f.DefValue)
		})
	}

	validDays := cmd.Flags().Lookup("valid-days")
	assert.NotNil(t, validDays, "--valid-days flag should exist")
	assert.Equal(t, "365", validDays.DefValue)
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single", "a.com", []string{"a.com"}},
		{"multiple", "a.com,b.com", []string{"a.com", "b.com"}},
		{"spaces around", " a.com , b.com ", []string{"a.com", "b.com"}},
		{"empty elements", "a.com,,b.com", []string{"a.com", "b.com"}},
		{"only commas", ",,,", []string{}},
		{"whitespace only", " , , ", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAndTrim(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFirstOf(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want string
	}{
		{"a non-empty", []string{"a.com", "b.com"}, []string{}, "a.com"},
		{"a empty b non-empty", []string{}, []string{"1.2.3.4"}, "1.2.3.4"},
		{"both non-empty returns a", []string{"a.com"}, []string{"1.2.3.4"}, "a.com"},
		{"both empty returns empty", []string{}, []string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, firstOf(tt.a, tt.b))
		})
	}
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
