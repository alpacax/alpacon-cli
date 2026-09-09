package token

import (
	"errors"
	"fmt"
	"sync"

	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/spf13/cobra"
)

const serverResolveConcurrency = 8

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

func resolveServerIDs(ac *client.AlpaconClient, names []string) ([]string, error) {
	serverIDs := make([]string, len(names))
	errorsByIndex := make([]error, len(names))
	var wg sync.WaitGroup
	indices := make(chan int)
	for range min(serverResolveConcurrency, len(names)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range indices {
				id, err := serverapi.GetServerIDByName(ac, names[idx])
				if err != nil {
					errorsByIndex[idx] = fmt.Errorf("failed to resolve server '%s': %w", names[idx], err)
				} else {
					serverIDs[idx] = id
				}
			}
		}()
	}
	for i := range names {
		indices <- i
	}
	close(indices)
	wg.Wait()
	for _, err := range errorsByIndex {
		if err != nil {
			return serverIDs, err
		}
	}
	return serverIDs, nil
}
