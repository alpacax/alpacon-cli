package cmd

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/pkg/selfupdate"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Display the current CLI version",
	Long:  "Displays the current version of the CLI and checks if there is an available update.",
	Run: func(cmd *cobra.Command, args []string) {
		utils.CliInfo("Current version: %s", utils.Version)
		release, err := selfupdate.LatestRelease(selfupdate.DefaultReleaseAPIURL)
		if err != nil {
			utils.CliWarning("Could not check for updates: %s", err)
			return
		}
		if selfupdate.IsOutdated(utils.Version, release.Version) {
			utils.CliWarning("%s", upgradeNotice(utils.Version, release.Version, release.HTMLURL))
			return
		}
		utils.CliInfo("You are up to date!")
	},
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
