package cmd

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/websh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valueFlagCase is a flag whose value is a separate token, with the arguments the parser needs
// around it to accept the line.
type valueFlagCase struct {
	flag, value string
	pre         []string
}

var execValueFlags = []valueFlagCase{
	{flag: "-u", value: "alice"},
	{flag: "--username", value: "alice"},
	{flag: "-g", value: "ops"},
	{flag: "--groupname", value: "ops"},
	{flag: "--work-session", value: "0b5e7c1a-0000-4000-8000-000000000000"},
	{flag: "--purpose", value: "check the disk"},
	{flag: "--output", value: "json"},
	{flag: "--file", value: "/opt/run.sh"},
	{flag: "--file-from", value: "run.sh", pre: []string{"--file", "/opt/run.sh"}},
	{flag: "--interpreter", value: "/bin/sh", pre: []string{"--file", "/opt/run.sh"}},
	{flag: "--reuse-days", value: "3", pre: []string{"--file", "/opt/run.sh"}},
	{flag: "--wait-approval", value: "30m"},
}

var websshValueFlags = []valueFlagCase{
	{flag: "-u", value: "alice"},
	{flag: "--username", value: "alice"},
	{flag: "-g", value: "ops"},
	{flag: "--groupname", value: "ops"},
	{flag: "--work-session", value: "0b5e7c1a-0000-4000-8000-000000000000"},
	{flag: "--output", value: "json"},
}

// Flags that take no separate token. --read-only is the one ownFlagArgs handles by hand.
var (
	execBareFlags   = []string{"-h", "--help", "--detach", "--wait", "--env=KEY=value"}
	websshBareFlags = []string{"-h", "--help", "-s", "--share", "--env=KEY=value", "--read-only"}
)

var flagTokenPattern = regexp.MustCompile(`-{1,2}[A-Za-z][A-Za-z-]*`)

// helpFlags reads the flag names a command documents under its "Flags:" heading: the part of
// each indented line before the first run of two spaces.
func helpFlags(text string) []string {
	var flags []string
	inFlags := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case line == "Flags:":
			inFlags = true
		case inFlags && line != "" && !strings.HasPrefix(line, " "):
			inFlags = false
		case inFlags && strings.HasPrefix(line, "  -"):
			header, _, _ := strings.Cut(strings.TrimPrefix(line, "  "), "  ")
			flags = append(flags, flagTokenPattern.FindAllString(header, -1)...)
		}
	}
	return flags
}

// Serial: the last subtest reads package-level Cobra commands.
func TestRemoteValueFlagsMatchTheExecAndWebshParsers(t *testing.T) {
	parsedValue := map[string]bool{}
	for _, c := range append(append([]valueFlagCase{}, execValueFlags...), websshValueFlags...) {
		parsedValue[c.flag] = true
	}

	t.Run("exec parser consumes each value flag as a separate token", func(t *testing.T) {
		t.Parallel()
		for _, c := range execValueFlags {
			args := append(append([]string{}, c.pre...), c.flag, c.value, "my-server")
			got := exec.ParseRemoteExecArgs(args)
			assert.Empty(t, got.Err, c.flag)
			assert.Equal(t, "my-server", got.Server, c.flag)
		}
	})
	t.Run("websh parser consumes each value flag as a separate token", func(t *testing.T) {
		t.Parallel()
		for _, c := range websshValueFlags {
			got, err := websh.ParseWebshArgs([]string{c.flag, c.value, "my-server"})
			require.NoError(t, err, c.flag)
			assert.Equal(t, "my-server", got.ServerName, c.flag)
			assert.Empty(t, got.CommandArgs, c.flag)
		}
	})
	t.Run("bare flags leave the next token to the server", func(t *testing.T) {
		t.Parallel()
		for _, flag := range execBareFlags[2:] {
			got := exec.ParseRemoteExecArgs([]string{flag, "my-server"})
			assert.Empty(t, got.Err, flag)
			assert.Equal(t, "my-server", got.Server, flag)
		}
		for _, flag := range websshBareFlags[2:] {
			got, err := websh.ParseWebshArgs([]string{flag, "my-server"})
			require.NoError(t, err, flag)
			assert.Equal(t, "my-server", got.ServerName, flag)
		}
	})

	t.Run("remoteValueFlags is exactly the value flags the parsers accept", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, sortedKeys(parsedValue), sortedKeys(remoteValueFlags))
	})

	t.Run("websshHoppedFlags is exactly websh's value flags", func(t *testing.T) {
		t.Parallel()
		want := map[string]bool{}
		for _, c := range websshValueFlags {
			want[c.flag] = true
		}
		assert.Equal(t, sortedKeys(want), sortedKeys(websshHoppedFlags))
	})

	t.Run("every flag the help documents is classified here", func(t *testing.T) {
		known := map[string]bool{}
		for _, c := range append(append([]valueFlagCase{}, execValueFlags...), websshValueFlags...) {
			known[c.flag] = true
		}
		for _, flag := range append(append([]string{}, execBareFlags...), websshBareFlags...) {
			known[strings.SplitN(flag, "=", 2)[0]] = true
		}
		for name, long := range map[string]string{"exec": exec.ExecCmd.Long, "websh": websh.WebshCmd.Example} {
			flags := helpFlags(long)
			require.NotEmpty(t, flags, name)
			for _, flag := range flags {
				assert.True(t, known[flag], "%s documents %s, which neither the value-flag nor the bare-flag list covers; classify it and update remoteValueFlags", name, flag)
			}
		}
	})
}

func TestWebshOwnArgsCutsWhereTheParserStartsTheCommand(t *testing.T) {
	t.Parallel()
	lines := [][]string{
		{"my-server", "--output", "json", "--read-only=true"},
		{"my-server", "--share", "ls", "--output", "json"},
		{"my-server", "-u", "alice", "--output=json", "ls", "-la"},
		{"my-server", "--read-only", "true", "--work-session", "ws-1", "uname"},
		{"-g", "ops", "my-server", "--env=KEY=1", "echo", "--output", "json"},
		{"my-server", "-x", "--output", "json"},
		{"my-server", "--", "--output", "json"},
		{"my-server"},
	}
	for _, args := range lines {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			parsed, err := websh.ParseWebshArgs(args)
			if err != nil {
				t.Skip("the parser stops early on an error; the whole line is own flags")
			}
			own := websshOwnArgs(args)
			assert.Equal(t, args[:len(args)-len(parsed.CommandArgs)], append([]string{}, own...))
		})
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
