package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"runtime"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/selfupdate"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCodeUpdateAvailableIsEight(t *testing.T) {
	assert.Equal(t, 8, utils.ExitCodeUpdateAvailable, "scripts branch on this value; it is a public contract")
}

func TestCheckOutcome(t *testing.T) {
	tests := []struct {
		name         string
		current      string
		latest       string
		wantContains []string
		wantCode     int
	}{
		{
			name:         "already newest",
			current:      "1.4.0",
			latest:       "1.4.0",
			wantContains: []string{"up to date", "1.4.0"},
			wantCode:     0,
		},
		{
			name:         "a newer release is out",
			current:      "1.3.0",
			latest:       "1.4.0",
			wantContains: []string{"1.3.0", "1.4.0"},
			wantCode:     utils.ExitCodeUpdateAvailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, code := checkOutcome(tt.current, tt.latest)

			assert.Equal(t, tt.wantCode, code)
			for _, want := range tt.wantContains {
				assert.Contains(t, message, want)
			}
		})
	}
}

func TestUpdateCommandIsRegistered(t *testing.T) {
	var names []string
	for _, command := range RootCmd.Commands() {
		names = append(names, command.Name())
	}

	assert.Contains(t, names, "update")
}

func TestPermissionHint(t *testing.T) {
	hint := permissionHint(os.ErrPermission)

	assert.Contains(t, hint, "not writable")
	if runtime.GOOS == "windows" {
		assert.Contains(t, hint, "administrator terminal", "Windows has no sudo to send the user to")
	} else {
		assert.Contains(t, hint, "sudo alpacon update")
	}
	assert.Empty(t, permissionHint(errors.New("connection reset")), "only a refused write earns the hint")
}

func TestPermissionHintStillNamesSudoWhenTheLockIsWhatWasRefused(t *testing.T) {
	hint := permissionHint(fmt.Errorf("%w: %w", selfupdate.ErrLockUnavailable, os.ErrPermission)) // The lock sits in the install directory, so this is that directory refusing a write—the first one the command attempts.

	assert.Contains(t, hint, "install location")
	if runtime.GOOS == "windows" {
		assert.Contains(t, hint, "administrator terminal")
	} else {
		assert.Contains(t, hint, "sudo alpacon update")
	}
}

func TestPermissionHintSendsATempDirectoryFailureSomewhereElse(t *testing.T) {
	hint := permissionHint(fmt.Errorf("%w: %w", selfupdate.ErrWorkDirUnavailable, os.ErrPermission))

	if runtime.GOOS == "windows" {
		assert.Contains(t, hint, "Point TMP at")
	} else {
		assert.Contains(t, hint, "Point TMPDIR at")
	}
	assert.NotContains(t, hint, "sudo alpacon update", "the variable naming the temp directory belongs to the user who set it")
}

func TestUpdateFailureMessageKeepsTheCauseBesideTheHint(t *testing.T) {
	refused := fmt.Errorf("%w: open /usr/local/bin/.alpacon-update.lock: %w", selfupdate.ErrLockUnavailable, os.ErrPermission)

	message := updateFailureMessage(refused)

	assert.Contains(t, message, "not writable", "the hint says what to do")
	assert.Contains(t, message, "/usr/local/bin/.alpacon-update.lock", "only the cause says which file refused")
	assert.Equal(t, "Update failed: connection reset", updateFailureMessage(errors.New("connection reset")))
}

func TestReportResultNeverReportsSuccessForAnInstallItLeftAlone(t *testing.T) {
	message, code := reportResult(selfupdate.Result{Kind: selfupdate.InstallVersionManager}, "1.3.0")

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.NotContains(t, message, "Successfully")
}

func TestReportResult(t *testing.T) {
	tests := []struct {
		name         string
		result       selfupdate.Result
		wantContains string
		wantCode     int
	}{
		{
			name:         "replaced",
			result:       selfupdate.Result{Kind: selfupdate.InstallManual, UpdatedTo: "1.4.0"},
			wantContains: "1.4.0",
			wantCode:     0,
		},
		{
			name:         "already newest",
			result:       selfupdate.Result{AlreadyCurrent: true},
			wantContains: "up to date",
			wantCode:     0,
		},
		{
			name:         "handed back to homebrew",
			result:       selfupdate.Result{Kind: selfupdate.InstallHomebrew, Guidance: "brew upgrade alpacon-cli"},
			wantContains: "brew upgrade alpacon-cli",
			wantCode:     utils.ExitCodeGeneralError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, code := reportResult(tt.result, "1.3.0")

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, message, tt.wantContains)
		})
	}
}

func TestCheckActionNeverSendsAPackageManagerInstallToTheUpdateCommand(t *testing.T) {
	assert.Contains(t, checkAction(selfupdate.InstallManual), "alpacon update")

	for _, kind := range []selfupdate.InstallKind{
		selfupdate.InstallHomebrew,
		selfupdate.InstallDeb,
		selfupdate.InstallRPM,
		selfupdate.InstallVersionManager,
	} {
		action := checkAction(kind)
		assert.NotContains(t, action, "alpacon update", "%s: 'alpacon update' exits 1 on this install, so --check must not name it", kind)
		assert.NotEmpty(t, action, "%s: exit 8 owes the caller something to do", kind)
	}
}

func TestUpdateCommandRefusesAPositionalArgument(t *testing.T) {
	require.NotNil(t, updateCmd.Args, "an unvalidated argument makes 'alpacon update check' replace the binary")

	assert.Error(t, updateCmd.Args(updateCmd, []string{"check"}))
	assert.NoError(t, updateCmd.Args(updateCmd, nil))
}

// The three tests below drive the real command in a child process: what Run
// decides after its first branch is an os.Exit, so deleting the dev-build guard
// or the os.Exit(8) leaves every in-process test green.
func TestUpdateCommandRefusesADevBuild(t *testing.T) {
	stderr, exitCode := runUpdateCommandHelper(t, []string{"update"})

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Contains(t, stderr, "matches no release")
	assert.NotContains(t, stderr, "Successfully updated")
}

func TestUpdateCommandCheckExitsSevenWhenANewerReleaseExists(t *testing.T) {
	stderr, exitCode := runUpdateCommandHelper(t, []string{"update", "--check"},
		"ALPACON_TEST_CURRENT_VERSION=1.0.0",
		"ALPACON_TEST_LATEST_RELEASE=9.9.9",
	)

	assert.Equal(t, utils.ExitCodeUpdateAvailable, exitCode, "scripts branch on 8; falling through to the end of Run would exit 0")
	assert.Contains(t, stderr, "1.0.0 -> 9.9.9")
}

func TestUpdateCommandCheckExitsZeroWhenAlreadyCurrent(t *testing.T) {
	stderr, exitCode := runUpdateCommandHelper(t, []string{"update", "--check"},
		"ALPACON_TEST_CURRENT_VERSION=9.9.9",
		"ALPACON_TEST_LATEST_RELEASE=9.9.9",
	)

	assert.Equal(t, 0, exitCode)
	assert.Contains(t, stderr, "already up to date")
}

func runUpdateCommandHelper(t *testing.T, args []string, env ...string) (stderr string, exitCode int) {
	t.Helper()

	helperArgs := append(
		[]string{"-test.run=^TestUpdateCommandHelperProcess$", "--", "update-helper"},
		args...,
	)
	helper := osexec.Command(os.Args[0], helperArgs...)
	helper.Env = append(os.Environ(), "GO_WANT_UPDATE_HELPER=1", homeEnvVar()+"="+t.TempDir())
	helper.Env = append(helper.Env, env...)

	var stdoutBuf, stderrBuf bytes.Buffer
	helper.Stdout = &stdoutBuf
	helper.Stderr = &stderrBuf

	err := helper.Run()
	exitCode = 0
	if err != nil {
		var exitErr *osexec.ExitError
		require.ErrorAs(t, err, &exitErr)
		exitCode = exitErr.ExitCode()
	}
	return stderrBuf.String(), exitCode
}

func TestUpdateCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_UPDATE_HELPER") != "1" {
		return
	}
	if latest := os.Getenv("ALPACON_TEST_LATEST_RELEASE"); latest != "" {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"tag_name":"v%s","html_url":"https://example.test/releases"}`, latest)
		}))
		defer ts.Close()
		selfupdate.DefaultReleaseAPIURL = ts.URL
		utils.Version = os.Getenv("ALPACON_TEST_CURRENT_VERSION")
	}

	args, ok := helperArgsAfter(os.Args, "update-helper")
	if !ok {
		os.Exit(2)
	}
	RootCmd.SetArgs(args)
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
