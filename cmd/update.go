package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/alpacax/alpacon-cli/pkg/selfupdate"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var checkOnly bool

var updateCmd = &cobra.Command{
	Use:   "update [flags]",
	Short: "Update the CLI to the latest release",
	Args:  cobra.NoArgs, // Without this, 'alpacon update check'—the typo for --check—is an argument nobody reads, and the binary gets replaced instead.
	Long: `Update the CLI to the latest release.

A binary a package manager or a version manager installed is left to that tool:
the command prints how to update through it instead of replacing the file. A
binary built from source carries the version 'dev', which matches no release,
and is refused.

--check installs nothing and carries its answer in the exit code, so a script
never has to parse prose:

  0  already on the latest release
  8  a newer release exists`,
	Example: `  alpacon update
  alpacon update --check`,
	Run: func(cmd *cobra.Command, args []string) {
		if selfupdate.IsUnknownVersion(utils.Version) {
			utils.CliErrorWithExit("This build's version %q matches no release, so there is nothing to update from. Install a released build to use 'alpacon update'.", utils.Version)
		}

		if checkOnly {
			release, err := selfupdate.LatestRelease(selfupdate.DefaultReleaseAPIURL)
			if err != nil {
				utils.CliErrorWithExit("Could not check for updates: %s", err)
			}
			message, code := checkOutcome(utils.Version, release.Version)
			utils.CliInfo("%s", message)
			if code != 0 {
				utils.CliInfo("%s", checkAction(installKind())) // CliInfo, not CliError: the check succeeded, and "Error" would contradict the exit code the README tells scripts to branch on.
				os.Exit(code)
			}
			return
		}

		executable, err := selfupdate.ResolveExecutablePath()
		if err != nil {
			utils.CliErrorWithExit("Could not find the running binary: %s", err)
		}

		result, err := selfupdate.Run(selfupdate.Options{
			ReleaseAPIURL:  selfupdate.DefaultReleaseAPIURL,
			CurrentVersion: utils.Version,
			GOOS:           runtime.GOOS,
			GOARCH:         runtime.GOARCH,
			ExecutablePath: executable,
			Runner:         selfupdate.ExecRunner,
		})
		if err != nil {
			utils.CliErrorWithExit("%s", updateFailureMessage(err))
		}

		message, code := reportResult(result, utils.Version)
		switch {
		case code != 0:
			utils.CliErrorWithExitCode(code, "%s", message)
		case result.AlreadyCurrent:
			utils.CliInfo("%s", message)
		default:
			utils.CliSuccess("%s", message) // The binary was replaced; Info would read exactly like the "already up to date" line above.
		}
	},
}

func init() {
	updateCmd.Flags().BoolVar(&checkOnly, "check", false, "Report whether a newer release exists without installing it")
}

func upToDateMessage(current string) string {
	return fmt.Sprintf("alpacon %s is already up to date.", current)
}

func checkOutcome(current, latest string) (string, int) {
	if !selfupdate.IsOutdated(current, latest) {
		return upToDateMessage(current), 0
	}
	return fmt.Sprintf("A newer release is available: %s -> %s.", current, latest), utils.ExitCodeUpdateAvailable
}

func installKind() selfupdate.InstallKind { // --check installs nothing, so a binary it cannot find or place has nothing better to say than manual—'alpacon update' is where not knowing has to stop the work.
	executable, err := selfupdate.ResolveExecutablePath()
	if err != nil {
		return selfupdate.InstallManual
	}
	kind, err := selfupdate.DetectInstallKind(selfupdate.ExecRunner, executable)
	if err != nil {
		return selfupdate.InstallManual
	}
	return kind
}

func checkAction(kind selfupdate.InstallKind) string { // Reads Kind rather than Guidance, for the reason reportResult gives below: an install kind whose wording nobody wrote must not be told to run 'alpacon update', which can only exit 1 on it.
	if kind == selfupdate.InstallManual {
		return "Run 'alpacon update' to install it."
	}
	return selfupdate.UpgradeGuidance(kind)
}

func updateFailureMessage(err error) string {
	if hint := permissionHint(err); hint != "" {
		return fmt.Sprintf("%s (%s)", hint, err)
	}
	if errors.Is(err, selfupdate.ErrOwnerUnknown) {
		return fmt.Sprintf("Could not tell whether a package manager owns this binary, so it was left alone. Try again once any package transaction has finished, or update through your package manager. (%s)", err)
	}
	return fmt.Sprintf("Update failed: %s", err)
}

// permissionHint names sudo for the user to run, never for the CLI to re-run
// itself: that would leave root-owned temp files and downloads in the user's own
// directories.
func permissionHint(err error) string {
	if !errors.Is(err, os.ErrPermission) {
		return ""
	}
	if errors.Is(err, selfupdate.ErrWorkDirUnavailable) { // The temp directory is nobody's install location, so sudo is not the answer: the variable naming it belongs to the user who set it.
		tempDirEnvVar := "TMPDIR"
		if runtime.GOOS == "windows" { // os.TempDir calls GetTempPath there, which reads TMP, TEMP and USERPROFILE and never TMPDIR.
			tempDirEnvVar = "TMP"
		}
		return fmt.Sprintf("The temporary directory is not writable by this user. Point %s at a writable directory and try again.", tempDirEnvVar)
	}
	if runtime.GOOS == "windows" {
		return "The install location is not writable by this user. Re-run 'alpacon update' from an administrator terminal."
	}
	return "The install location is not writable by this user. Re-run as 'sudo alpacon update'."
}

// reportResult reads Kind, not Guidance: an install kind whose wording nobody
// wrote would otherwise be called a success and print "Successfully updated
// to ." having replaced nothing.
func reportResult(result selfupdate.Result, current string) (string, int) {
	switch {
	case result.AlreadyCurrent:
		return upToDateMessage(current), 0
	case result.Kind != selfupdate.InstallManual:
		return result.Guidance, utils.ExitCodeGeneralError
	default:
		return fmt.Sprintf("Successfully updated to %s.", result.UpdatedTo), 0
	}
}
