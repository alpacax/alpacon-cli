package token

import (
	"errors"
	"fmt"
	"sync"

	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/spf13/cobra"
)

var aclServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage server ACL rules for a token",
	Long: `Control which servers an API token can access.

Deny-by-default: if no server ACL exists for a token, access to all servers is denied.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = cmd.Help()
		return errors.New("a subcommand is required")
	},
}

func init() {
	aclServerCmd.AddCommand(aclServerAddCmd)
	aclServerCmd.AddCommand(aclServerListCmd)
	aclServerCmd.AddCommand(aclServerDeleteCmd)
}

// maxConcurrentServerLookups caps the fan-out: service tokens carry an hourly request quota.
const maxConcurrentServerLookups = 8

func resolveServerIDs(ac *client.AlpaconClient, names []string) ([]string, error) {
	serverIDs := make([]string, len(names))
	errs := make([]error, len(names))
	sem := make(chan struct{}, maxConcurrentServerLookups)
	var wg sync.WaitGroup

	for i, name := range names {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			id, err := serverapi.GetServerIDByName(ac, name)
			if err != nil {
				errs[i] = fmt.Errorf("failed to resolve server '%s': %w", name, err)
				return
			}
			serverIDs[i] = id
		})
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return serverIDs, err
		}
	}
	return serverIDs, nil
}
