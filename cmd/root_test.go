package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWelcomeLines(t *testing.T) {
	t.Run("not logged in (no config file)", func(t *testing.T) {
		setTestHome(t, t.TempDir())
		lines := buildWelcomeLines()
		require.Len(t, lines, 3)
		assert.Contains(t, lines[0], "alpacon")
		assert.Contains(t, lines[1], "Not logged in")
		assert.Contains(t, lines[2], "--help")
	})

	t.Run("logged in via Auth0 — workspace host shown", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://myws.us1.alpacon.io","workspace_name":"myws","access_token":"a"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		require.Len(t, lines, 3)
		assert.Equal(t, "myws.us1.alpacon.io", lines[1])
	})

	t.Run("logged in via legacy token — workspace host shown", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://myws.alpacon.io","workspace_name":"myws","token":"t"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		assert.Equal(t, "myws.alpacon.io", lines[1])
	})

	t.Run("config malformed — surfaces config error, not 'not logged in'", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not-json"), 0600))

		lines := buildWelcomeLines()
		assert.Contains(t, lines[1], "Config read error")
		assert.NotContains(t, lines[1], "Not logged in")
	})

	t.Run("config exists but no tokens — treated as not logged in", func(t *testing.T) {
		home := t.TempDir()
		setTestHome(t, home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://x.alpacon.io","workspace_name":"x"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		assert.Contains(t, lines[1], "Not logged in")
	})
}

func TestHostFromURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"https with subdomain", "https://myws.us1.alpacon.io", "myws.us1.alpacon.io"},
		{"http", "http://example.com", "example.com"},
		{"with path and query", "https://myws.alpacon.io/some/path?x=1", "myws.alpacon.io"},
		{"with port", "https://localhost:8080", "localhost:8080"},
		{"no scheme falls back to raw input", "myws.alpacon.io", "myws.alpacon.io"},
		{"empty", "", ""},
		{"malformed url with scheme returns input", "https://", "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hostFromURL(tt.input))
		})
	}
}

func TestRootRegistersAPICommand(t *testing.T) {
	command, _, err := RootCmd.Find([]string{"api"})

	require.NoError(t, err)
	assert.Equal(t, "api", command.Name())
}

func TestRootVersionFlagBehavesLikeTheVersionCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		cmd   []string
		flags map[string][]string
	}{
		{
			name:  "table",
			cmd:   []string{"version"},
			flags: map[string][]string{"long": {"--version"}, "shorthand": {"-v"}},
		},
		{
			name:  "json",
			cmd:   []string{"version", "--output", "json"},
			flags: map[string][]string{"long": {"--version", "--output", "json"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := func(t *testing.T, args []string) (string, string, int) {
				t.Helper()
				return runHelperProcess(t, "TestUpdateCommandHelperProcess", "update-helper", args,
					"GO_WANT_UPDATE_HELPER=1", homeEnvVar()+"="+t.TempDir(),
					"ALPACON_TEST_LATEST_RELEASE=1.4.0", "ALPACON_TEST_CURRENT_VERSION=1.3.0")
			}

			wantOut, wantErr, wantCode := run(t, tc.cmd)
			require.Contains(t, wantOut+wantErr, "1.4.0", "the version command must have reached the stub release")

			for name, flag := range tc.flags {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					gotOut, gotErr, gotCode := run(t, flag)

					assert.Equal(t, wantCode, gotCode)
					assert.Equal(t, wantOut, gotOut)
					assert.Equal(t, wantErr, gotErr)
				})
			}
		})
	}
}

type exitHookErrors struct{ persistentPre, pre, run, post, persistentPost error }

func newExitTestTree(hooks exitHookErrors, out io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use: "tool", SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE:  func(*cobra.Command, []string) error { return hooks.persistentPre },
		PersistentPostRunE: func(*cobra.Command, []string) error { return hooks.persistentPost },
	}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("output", "table", "")
	leaf := &cobra.Command{
		Use: "leaf", Args: cobra.ExactArgs(1),
		PreRunE:  func(*cobra.Command, []string) error { return hooks.pre },
		RunE:     func(*cobra.Command, []string) error { return hooks.run },
		PostRunE: func(*cobra.Command, []string) error { return hooks.post },
	}
	leaf.Flags().Int("count", 0, "")
	root.AddCommand(leaf)
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(group)
	required := &cobra.Command{Use: "required", Run: func(*cobra.Command, []string) {}}
	required.Flags().String("name", "", "")
	_ = required.MarkFlagRequired("name")
	root.AddCommand(required)
	root.InitDefaultCompletionCmd()
	markHookErrors(root)
	return root
}

var errBoom = errors.New("boom")

func TestMarkHookErrorsTellsAHookErrorFromCobrasOwn(t *testing.T) {
	t.Parallel()
	usage := utils.MarkUsageError(errors.New("bad"))
	tests := []struct {
		name      string
		args      []string
		hooks     exitHookErrors
		wantHook  bool
		wantUsage bool
	}{
		{name: "unknown flag", args: []string{"leaf", "--bogus", "x"}},
		{name: "bad int flag value", args: []string{"leaf", "--count", "x", "a"}},
		{name: "flag needs an argument", args: []string{"leaf", "--count"}},
		{name: "argument count mismatch", args: []string{"leaf"}},
		{name: "missing required flag", args: []string{"required"}},
		{name: "unknown subcommand of a no-args group", args: []string{"group", "bogus"}},
		{name: "unknown command", args: []string{"bogus"}},
		{name: "RunE returns a plain error", args: []string{"leaf", "a"}, hooks: exitHookErrors{run: errBoom}, wantHook: true},
		{name: "RunE returns a usage error", args: []string{"leaf", "a"}, hooks: exitHookErrors{run: usage}, wantHook: true, wantUsage: true},
		{name: "RunE returns a joined usage error", args: []string{"leaf", "a"}, hooks: exitHookErrors{run: errors.Join(errors.New("ctx"), usage)}, wantHook: true, wantUsage: true},
		{name: "PersistentPreRunE returns a plain error", args: []string{"leaf", "a"}, hooks: exitHookErrors{persistentPre: errBoom}, wantHook: true},
		{name: "PersistentPreRunE returns a usage error", args: []string{"leaf", "a"}, hooks: exitHookErrors{persistentPre: usage}, wantHook: true, wantUsage: true},
		{name: "PreRunE returns a plain error", args: []string{"leaf", "a"}, hooks: exitHookErrors{pre: errBoom}, wantHook: true},
		{name: "PreRunE returns a usage error", args: []string{"leaf", "a"}, hooks: exitHookErrors{pre: usage}, wantHook: true, wantUsage: true},
		{name: "PostRunE returns a plain error", args: []string{"leaf", "a"}, hooks: exitHookErrors{post: errBoom}, wantHook: true},
		{name: "PostRunE returns a usage error", args: []string{"leaf", "a"}, hooks: exitHookErrors{post: usage}, wantHook: true, wantUsage: true},
		{name: "PersistentPostRunE returns a plain error", args: []string{"leaf", "a"}, hooks: exitHookErrors{persistentPost: errBoom}, wantHook: true},
		{name: "PersistentPostRunE returns a usage error", args: []string{"leaf", "a"}, hooks: exitHookErrors{persistentPost: usage}, wantHook: true, wantUsage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := newExitTestTree(tt.hooks, io.Discard)
			root.SetArgs(tt.args)

			err := root.Execute()

			require.Error(t, err)
			assert.Equal(t, tt.wantHook, errors.As(err, new(hookError)))
			assert.Equal(t, tt.wantUsage, utils.IsUsageError(err))
		})
	}
}

type failingWriter struct{}

var errWrite = errors.New("write failed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestMarkHookErrorsTreatsACompletionWriteFailureAsAHookFailure(t *testing.T) {
	t.Parallel()
	root := newExitTestTree(exitHookErrors{}, failingWriter{})
	root.SetArgs([]string{"completion", "bash"})

	err := root.Execute()

	require.ErrorIs(t, err, errWrite)
	require.ErrorAs(t, err, new(hookError))
	assert.False(t, utils.IsUsageError(err), "a write failure is not the command line's fault: %v", err)
}

func TestMarkHookErrorsLeavesSuccessAlone(t *testing.T) {
	t.Parallel()
	root := newExitTestTree(exitHookErrors{}, io.Discard)
	root.SetArgs([]string{"leaf", "a"})

	assert.NoError(t, root.Execute())
}

func TestMarkHookErrorsKeepsTheCause(t *testing.T) {
	t.Parallel()
	root := newExitTestTree(exitHookErrors{run: errBoom}, io.Discard)
	root.SetArgs([]string{"leaf", "a"})

	err := root.Execute()

	require.ErrorIs(t, err, errBoom)
	assert.EqualError(t, err, "boom")
}

func newOwnParserTree() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	root.PersistentFlags().String("output", "table", "")
	run := &cobra.Command{Use: "run", DisableFlagParsing: true, Run: func(*cobra.Command, []string) {}}
	ls := &cobra.Command{Use: "ls", Aliases: []string{"list"}, Run: func(*cobra.Command, []string) {}}
	ls.Flags().String("server", "", "")
	run.AddCommand(ls)
	root.AddCommand(run)
	return root
}

func TestKeepWithOwnParserHandsAnUnknownFlagBeforeASubcommandToTheParent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "subcommand alone", args: []string{"run", "ls"}, want: "ls"},
		{name: "subcommand alias", args: []string{"run", "list"}, want: "ls"},
		{name: "subcommand flag after its name", args: []string{"run", "ls", "--server", "my-server"}, want: "ls"},
		{name: "known flag before the subcommand", args: []string{"run", "--output", "json", "ls"}, want: "ls"},
		{name: "known flag ahead of the parent", args: []string{"--output", "json", "run", "ls"}, want: "ls"},
		{name: "unknown flag that took the server", args: []string{"run", "--bogus", "my-server", "ls"}, want: "run"},
		{name: "subcommand flag before its name", args: []string{"run", "--server", "my-server", "ls"}, want: "run"},
		{name: "unknown flag joined by equals", args: []string{"run", "--bogus=x", "ls"}, want: "run"},
		{name: "unknown shorthand", args: []string{"run", "-s", "my-server", "list"}, want: "run"},
		{name: "unknown flag after a leading known one", args: []string{"--output", "json", "run", "--bogus", "my-server", "ls"}, want: "run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := newOwnParserTree()

			keepWithOwnParser(root, tt.args)

			cmd, _, err := root.Find(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cmd.Name())
		})
	}
}
