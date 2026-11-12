package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/selfupdate"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpgradeNoticeNamesBothVersionsAndTheUpdateCommand(t *testing.T) {
	t.Parallel()
	notice := upgradeNotice("1.3.0", "1.4.0", "https://example.test/notes")

	assert.Contains(t, notice, "1.3.0")
	assert.Contains(t, notice, "1.4.0")
	assert.Contains(t, notice, "https://example.test/notes")
	assert.Contains(t, notice, "alpacon update")
}

func TestUpgradeNoticeDoesNotSendALocalBuildToTheUpdateCommand(t *testing.T) {
	t.Parallel()
	notice := upgradeNotice(utils.DevVersion, "1.4.0", "https://example.test/notes")

	assert.NotContains(t, notice, "alpacon update", "'alpacon update' refuses a local build, so the notice must not name it")
	assert.Contains(t, notice, "Install a released build")
}

func TestUpgradeNoticeSaysWhatAManagedInstallWillGetInstead(t *testing.T) {
	t.Parallel()
	notice := upgradeNotice("1.3.0", "1.4.0", "https://example.test/notes")

	assert.Contains(t, notice, "an install another tool owns")
}

func TestNewVersionOutputReportsTheLatestReleaseAndWhetherItIsNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		current         string
		latest          string
		updateAvailable bool
	}{
		{name: "up to date", current: "1.4.0", latest: "1.4.0", updateAvailable: false},
		{name: "newer release", current: "1.3.0", latest: "1.4.0", updateAvailable: true},
		{name: "ahead of the release", current: "1.5.0", latest: "1.4.0", updateAvailable: false},
		{name: "local build", current: utils.DevVersion, latest: "1.4.0", updateAvailable: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			release := &selfupdate.Release{Version: tc.latest, HTMLURL: "https://example.test/notes"}

			out := newVersionOutput(tc.current, release)

			assert.Equal(t, tc.current, out.Version)
			require.NotNil(t, out.Latest)
			assert.Equal(t, tc.latest, *out.Latest)
			require.NotNil(t, out.ReleaseURL)
			assert.Equal(t, "https://example.test/notes", *out.ReleaseURL)
			assert.Equal(t, tc.updateAvailable, out.UpdateAvailable)
		})
	}
}

func TestNewVersionOutputKeepsTheCurrentVersionWhenNoReleaseWasRead(t *testing.T) {
	t.Parallel()

	out := newVersionOutput("1.4.0", nil)

	rendered, err := utils.FormatJSON(out)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":"1.4.0","latest":null,"update_available":false,"release_url":null}`, rendered)
}

func TestVersionCommandPrintsJSONOnStdoutWithOutputJSON(t *testing.T) {
	stdout, stderr, exitCode := runVersionCommand(t, []string{"version", "--output", "json"}, false)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.JSONEq(t, `{"version":"1.3.0","latest":"1.4.0","update_available":true,"release_url":"https://example.test/releases"}`, stdout)
	assert.Empty(t, stderr)
}

func TestVersionCommandPrintsTheCurrentVersionAndExitsZeroWhenTheReleaseLookupFails(t *testing.T) {
	stdout, stderr, exitCode := runVersionCommand(t, []string{"version", "--output", "json"}, true)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.JSONEq(t, `{"version":"1.3.0","latest":null,"update_available":false,"release_url":null}`, stdout)
	assert.Contains(t, stderr, "Could not check for updates")
	assert.NotContains(t, stderr, "{")
}

func runVersionCommand(t *testing.T, args []string, fails bool) (stdout, stderr string, exitCode int) {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.4.0","html_url":"https://example.test/releases"}`)
	}))
	defer ts.Close()

	oldReleaseAPIURL, oldVersion := selfupdate.DefaultReleaseAPIURL, utils.Version
	oldOutputFormat := utils.OutputFormat
	selfupdate.DefaultReleaseAPIURL, utils.Version = ts.URL, "1.3.0"
	t.Cleanup(func() {
		selfupdate.DefaultReleaseAPIURL, utils.Version = oldReleaseAPIURL, oldVersion
		utils.OutputFormat = oldOutputFormat
	})

	var err error
	stdout, stderr = testutil.CaptureOutput(t, func() {
		err = executeRootCommand(t, args)
	})
	if err == nil {
		return stdout, stderr, 0
	}
	exitErr, ok := errors.AsType[*utils.ExitError](err)
	require.True(t, ok)
	return stdout, stderr, exitErr.Code
}

// executeRootCommand parses args with RootCmd's own flags, so a broken flag registration fails the
// caller, and puts every flag it set back to its default afterwards.
func executeRootCommand(t *testing.T, args []string) error {
	t.Helper()
	RootCmd.SetArgs(args)
	t.Cleanup(func() {
		RootCmd.SetArgs(nil)
		resetChangedFlags(t, RootCmd)
	})
	_, err := RootCmd.ExecuteC()
	return err
}

func resetChangedFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	for _, flags := range []*pflag.FlagSet{cmd.LocalNonPersistentFlags(), cmd.PersistentFlags()} {
		flags.VisitAll(func(flag *pflag.Flag) {
			if flag.Changed {
				require.NoError(t, flag.Value.Set(flag.DefValue))
				flag.Changed = false
			}
		})
	}
	for _, child := range cmd.Commands() {
		resetChangedFlags(t, child)
	}
}
