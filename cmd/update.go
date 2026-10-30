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
	Use:   "update",
	Short: "Update the CLI to the latest release",
	Args:  cobra.NoArgs, // 'alpacon update check', a typo for --check, must fail, not replace the binary.
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
				utils.CliInfo("%s", checkAction(installKind())) // The check succeeded; "Error" would contradict the exit code scripts read.
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
			utils.CliSuccess("%s", message) // Info would read like the "already up to date" line.
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

func installKind() selfupdate.InstallKind { // --check installs nothing, so an unresolvable binary reads as manual.
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

func checkAction(kind selfupdate.InstallKind) string { // Reads Kind, not Guidance: a kind with no wording must not be sent to 'alpacon update'.
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
	if errors.Is(err, selfupdate.ErrWorkDirUnavailable) { // The temp dir is no install location: the fix is the user's variable, not sudo.
		tempDirEnvVar := "TMPDIR"
		if runtime.GOOS == "windows" { // os.TempDir reads TMP, TEMP and USERPROFILE there, never TMPDIR.
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
