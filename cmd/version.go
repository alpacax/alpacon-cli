package cmd

import (
	"context"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/google/go-github/github"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Displays the current CLI version.",
	Long:  "Displays the current version of the CLI and checks if there is an available update.",
	Run: func(cmd *cobra.Command, args []string) {
		utils.CliInfo("Current version: %s", utils.Version)
		release, skip := versionCheck()
		if !skip {
			utils.CliWarning("Upgrade available. Current version: %s. Latest version: %s \n"+
				"Visit %s for update instructions and release notes.", utils.Version, strings.TrimPrefix(release.GetTagName(), "v"), release.GetHTMLURL())
			return
		} else {
			utils.CliInfo("You are up to date! %s is the latest version available.", utils.Version)
		}
	},
}

func versionCheck() (*github.RepositoryRelease, bool) {
	client := github.NewClient(nil)
	ctx := context.Background()

	release, _, err := client.Repositories.GetLatestRelease(ctx, "alpacax", "alpacon-cli")
	if err != nil {
		utils.CliError("Checking for a newer version failed with: %s. \n", err)
		return nil, true
	}
	releaseVersion := strings.TrimPrefix(release.GetTagName(), "v")
	if releaseVersion != utils.Version {
		return release, false
	}

	return release, true
}
