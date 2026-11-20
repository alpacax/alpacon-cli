package cmd

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/pkg/selfupdate"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newVersionCmd(ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Display the current CLI version",
		Long:  "Displays the current version of the CLI and checks if there is an available update.",
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runVersion(ios)
		}),
	}
}

func runVersion(ios *utils.IOStreams) error {
	if ios.IsJSON() {
		return printVersionJSON(ios)
	}
	ios.Info("Current version: %s", utils.Version)
	release, err := selfupdate.LatestRelease(selfupdate.DefaultReleaseAPIURL)
	if err != nil {
		ios.Warning("Could not check for updates: %s", err)
		return nil
	}
	if selfupdate.IsOutdated(utils.Version, release.Version) {
		ios.Warning("%s", upgradeNotice(utils.Version, release.Version, release.HTMLURL))
		return nil
	}
	ios.Info("You are up to date!")
	return nil
}

// A released build is sent to 'alpacon update' whoever owns it: asking which
// package manager does would spawn dpkg and rpm on every 'alpacon version', so
// the caveat stands in for the answer.
func upgradeNotice(current, latest, htmlURL string) string {
	action := "Run 'alpacon update' to install it—on an install another tool owns it prints how to update through that tool instead."
	if selfupdate.IsUnknownVersion(current) {
		action = "Install a released build to update."
	}
	return fmt.Sprintf("Upgrade available: %s -> %s\n%s Release notes: %s", current, latest, action, htmlURL)
}

type versionOutput struct {
	Version         string  `json:"version"`
	Latest          *string `json:"latest"`
	UpdateAvailable bool    `json:"update_available"`
	ReleaseURL      *string `json:"release_url"`
}

func newVersionOutput(current string, release *selfupdate.Release) versionOutput {
	out := versionOutput{Version: current}
	if release != nil {
		out.Latest = &release.Version
		out.ReleaseURL = &release.HTMLURL
		out.UpdateAvailable = !selfupdate.IsUnknownVersion(current) && selfupdate.IsOutdated(current, release.Version)
	}
	return out
}

// Exits 0 even when the lookup fails; scripts branch on 'alpacon update --check'.
func printVersionJSON(ios *utils.IOStreams) error {
	release, err := selfupdate.LatestRelease(selfupdate.DefaultReleaseAPIURL)
	if err != nil {
		ios.Warning("Could not check for updates: %s", err)
	}
	if err := utils.PrintJSONValue(ios.Out, newVersionOutput(utils.Version, release)); err != nil {
		return utils.NewExitError("Failed to print the version: %s", err)
	}
	return nil
}
