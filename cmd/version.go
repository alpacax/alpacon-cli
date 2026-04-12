package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Display the current CLI version",
	Long:  "Displays the current version of the CLI and checks if there is an available update.",
	Run: func(cmd *cobra.Command, args []string) {
		utils.CliInfo("Current version: %s", utils.Version)
		tagName, htmlURL, err := getLatestRelease()
		if err != nil {
			utils.CliWarning("Could not check for updates: %s", err)
			return
		}
		releaseVersion := strings.TrimPrefix(tagName, "v")
		if releaseVersion != utils.Version {
			utils.CliWarning("Upgrade available: %s -> %s\nVisit %s for release notes.", utils.Version, releaseVersion, htmlURL)
		} else {
			utils.CliInfo("You are up to date!")
		}
	},
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func getLatestRelease() (tagName, htmlURL string, err error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/alpacax/alpacon-cli/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", utils.GetUserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return "", "", err
	}

	return release.TagName, release.HTMLURL, nil
}
