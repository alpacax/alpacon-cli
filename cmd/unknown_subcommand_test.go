package cmd

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestWordNamingNoSubcommandExitsTwoAsUnknownCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "workspace", args: []string{"ws", "swich", "prod"}, want: []string{`unknown command "swich" for "alpacon workspace"`, "Did you mean this?\n\tswitch\n"}},
		{name: "workspace preferences", args: []string{"workspace", "preferences", "updat"}, want: []string{`unknown command "updat" for "alpacon workspace preferences"`, "Did you mean this?\n\tupdate\n"}},
		{name: "workspace access-control", args: []string{"workspace", "access-control", "updte"}, want: []string{`unknown command "updte" for "alpacon workspace access-control"`, "Did you mean this?\n\tupdate\n"}},
		{name: "workspace authentication", args: []string{"workspace", "authentication", "upate"}, want: []string{`unknown command "upate" for "alpacon workspace authentication"`, "Did you mean this?\n\tupdate\n"}},
		{name: "event", args: []string{"event", "wach"}, want: []string{`unknown command "wach" for "alpacon event"`, "Did you mean this?\n\twait\n\twatch\n"}},
		{name: "group that prints help without a subcommand", args: []string{"token", "lst"}, want: []string{`unknown command "lst" for "alpacon token"`, "Did you mean this?\n\tls\n"}},
		{name: "server group", args: []string{"server", "lst"}, want: []string{`unknown command "lst" for "alpacon server"`, "Did you mean this?\n\tls\n"}},
		{name: "kube", args: []string{"kube", "lst"}, want: []string{`unknown command "lst" for "alpacon kube"`, "Did you mean this?\n\tls\n"}},
		{name: "token acl server", args: []string{"token", "acl", "server", "lst"}, want: []string{`unknown command "lst"`, "Did you mean this?\n\tls\n"}},
		{name: "workspace a", args: []string{"workspace", "a"}, want: []string{`unknown command "a"`, "Did you mean this?\n\taccess-control\n\tauthentication\n\tls\n"}},
		{name: "approval guidance", args: []string{"approval", "deny", "550e8400-e29b-41d4-a716-446655440000"}, want: []string{"Approval and rejection happen in the Alpacon console (web) or Slack."}},
		{name: "work session guidance", args: []string{"work-session", "deny", "550e8400-e29b-41d4-a716-446655440000"}, want: []string{"Approval and rejection happen in the Alpacon console (web) or Slack."}},
		{name: "word with no close subcommand", args: []string{"workspace", "zzzzzz"}, want: []string{`unknown command "zzzzzz" for "alpacon workspace"` + "\nRun 'alpacon workspace --help' for usage."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, exitCode := runHelperProcess(t, "TestExitCodeHelperProcess", exitHelperMarker, tt.args,
				exitHelperEnv+"=1", homeEnvVar()+"="+t.TempDir())

			assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
			assert.Empty(t, stdout)
			for _, want := range tt.want {
				assert.Contains(t, stderr, want)
			}
			assert.NotContains(t, stderr, "a subcommand is required")
		})
	}
}

func TestRunnableParentWithoutAWordStillRuns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantStdout string
		wantStderr string
	}{
		{name: "event with flags", args: []string{"event", "--tail", "10"}, wantStdout: "SERVER"},
		{name: "workspace", args: []string{"workspace"}, wantStderr: "Current workspace: ws"},
		{name: "workspace preferences", args: []string{"workspace", "preferences"}, wantStdout: `"count"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, exitCode := runHelperProcess(t, "TestExitCodeHelperProcess", exitHelperMarker, tt.args,
				exitHelperEnv+"=1", homeEnvVar()+"="+t.TempDir())

			assert.Equal(t, 0, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
			assert.Contains(t, stdout, tt.wantStdout)
			assert.Contains(t, stderr, tt.wantStderr)
			assert.NotContains(t, stderr, "unknown command")
		})
	}
}

func TestHelpFlagWinsOverAWordThatNamesNoSubcommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runHelperProcess(t, "TestExitCodeHelperProcess", exitHelperMarker, []string{"workspace", "swich", "--help"},
		exitHelperEnv+"=1", homeEnvVar()+"="+t.TempDir())

	assert.Equal(t, 0, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "Available Commands:")
	assert.NotContains(t, stdout+stderr, "unknown command")
}
