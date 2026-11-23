package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildRoot(ios *utils.IOStreams) *cobra.Command { return newRootCmd(cmdtest.NoClient, ios) }

func TestHelpUnderJSONGoesToStderr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "exec flag", args: []string{"exec", "--help", "--output", "json"}},
		{name: "exec output before help", args: []string{"exec", "--output=json", "-h"}},
		{name: "exec output after an invalid flag", args: []string{"exec", "--help", "--bogus", "--output", "json"}},
		{name: "websh output after an invalid flag", args: []string{"websh", "--help", "--bogus", "--output", "json"}},
		{name: "exec output after two invalid flags", args: []string{"exec", "-h", "--bogus", "--wait=x", "--output=json"}},
		{name: "websh output after server", args: []string{"websh", "-h", "my-server", "--output", "json"}},
		{name: "websh flag", args: []string{"websh", "--help", "--output", "json"}},
		{name: "websh output after read-only and a server", args: []string{"websh", "-h", "--read-only", "false", "my-server", "--output", "json"}},
		{name: "group flag", args: []string{"token", "--help", "--output", "json"}},
		{name: "leaf flag", args: []string{"token", "ls", "--help", "--output", "json"}},
		{name: "help command", args: []string{"help", "token", "--output", "json"}},
		{name: "group without subcommand", args: []string{"token", "--output", "json"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := cmdtest.RunCaptured(buildRoot, tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "--output")
		})
	}
}

func TestHelpUnderTableGoesToStdout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
	}{
		{name: "group flag", args: []string{"token", "--help"}, wantStdout: "--output"},
		{name: "leaf flag", args: []string{"token", "ls", "--help"}, wantStdout: "--output"},
		{name: "group without subcommand", args: []string{"token"}, wantCode: 2, wantStdout: "--output"},
		{name: "exec flag value names json", args: []string{"exec", "--help", "-u", "--output=json"}, wantStdout: "--output"},
		{name: "websh flag value names json", args: []string{"websh", "--help", "-u", "--output=json"}, wantStdout: "--output"},
		{name: "exec remote command output flag", args: []string{"exec", "-h", "my-server", "cmd", "--output", "json"}, wantStdout: "--output"},
		{name: "exec remote command output flag after an invalid flag", args: []string{"exec", "-h", "--bogus", "my-server", "cmd", "--output", "json"}, wantStdout: "--output"},
		{name: "websh remote command output flag after an empty work session", args: []string{"websh", "-h", "my-server", "--work-session=", "cmd", "--output", "json"}, wantStdout: "--output"},
		{name: "websh remote command output flag", args: []string{"websh", "-h", "my-server", "cmd", "--output", "json"}, wantStdout: "--output"},
		{name: "output topic", args: []string{"help", "output"}, wantStdout: "Stdout carries data only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := cmdtest.RunCaptured(buildRoot, tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, stdout, tt.wantStdout)
			assert.NotContains(t, stderr, "--output")
		})
	}
}

func TestHelpPrintsNoUnsetEnvWarning(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"exec", "--help", "--env=ALPACON_TEST_UNSET"},
		{"exec", "--env=ALPACON_TEST_UNSET", "--help"},
		{"websh", "--help", "--env=ALPACON_TEST_UNSET"},
		{"websh", "--env=ALPACON_TEST_UNSET", "--help"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := cmdtest.RunCaptured(buildRoot, args...)

			assert.Equal(t, 0, code)
			assert.Contains(t, stdout, "--output")
			assert.Empty(t, stderr)
		})
	}
}

func TestOutputHelpListsEverySyntheticErrorCode(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "../utils/error_codes.go", nil, 0)
	require.NoError(t, err)
	var codes []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			if !strings.HasPrefix(value.Names[0].Name, "ErrorCode") {
				continue
			}
			code, err := strconv.Unquote(value.Values[0].(*ast.BasicLit).Value)
			require.NoError(t, err)
			codes = append(codes, code)
		}
	}
	require.NotEmpty(t, codes, "no ErrorCode constants found; the walk matched nothing")

	long := newOutputHelpCmd().Long

	for _, code := range codes {
		assert.Contains(t, long, code)
	}
}
