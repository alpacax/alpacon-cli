package cmd

import (
	"context"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Displays the current CLI version.",
	Long:  "Displays the current version of the CLI and checks if there is an available update.",
	Run: func(cmd *cobra.Command, args []string) {
		utils.CliInfo("Current version: %s", utils.Version)
		release, err := getLatestRelease()
		if err != nil {
			utils.CliWarning("Could not check for updates: %s", err)
			return
		}
		releaseVersion := strings.TrimPrefix(release.GetTagName(), "v")
		if releaseVersion != utils.Version {
			utils.CliWarning("Upgrade available: %s -> %s\nVisit %s for release notes.", utils.Version, releaseVersion, release.GetHTMLURL())
		} else {
			utils.CliInfo("You are up to date!")
		}
	},
}

func getLatestRelease() (*github.RepositoryRelease, error) {
	ghClient := github.NewClient(nil)
	ctx := context.Background()
	release, _, err := ghClient.Repositories.GetLatestRelease(ctx, "alpacax", "alpacon-cli")
	return release, err
}
