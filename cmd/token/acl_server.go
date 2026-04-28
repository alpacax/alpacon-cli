package token

import (
	"errors"
	"fmt"
	"sync"

	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/spf13/cobra"
)

func resolveServerIDs(ac *client.AlpaconClient, names []string) ([]string, error) {
	serverIDs := make([]string, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for i, name := range names {
		wg.Add(1)
		go func(idx int, n string) {
			defer wg.Done()
			id, err := serverapi.GetServerIDByName(ac, n)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to resolve server '%s': %w", n, err)
				}
				return
			}
			serverIDs[idx] = id
		}(i, name)
	}
	wg.Wait()

	return serverIDs, firstErr
}

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
