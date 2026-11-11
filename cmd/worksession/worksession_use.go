package worksession

import (
	"errors"
	"fmt"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var unsetActiveWorkSession bool

var workSessionUseCmd = &cobra.Command{
	Use:   "use [SESSION_ID]",
	Short: "Set or clear the active work-session",
	Long: `Set the active work-session for the current workspace by passing its SESSION_ID.
Subsequent exec/websh/cp/tunnel commands attach to this session unless overridden with --work-session.
Pass --unset (with no SESSION_ID) to clear the active work-session.`,
	Example: `  alpacon work-session use 550e8400-e29b-41d4-a716-446655440000
  alpacon work-session use --unset`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		if unsetActiveWorkSession {
			if len(args) > 0 {
				return utils.NewUsageEnvelopeExitError(opUnset, "--unset cannot be combined with a SESSION_ID argument")
			}
			// A missing config, workspace, or entry counts as already unset, so --unset never fails
			// on it.
			if cur, err := config.GetActiveWorkSession(); err != nil || cur == "" {
				if utils.OutputFormat == utils.OutputFormatJSON {
					return printWorkSessionMutationJSON(workSessionMutationOutput{
						OK:                true,
						Operation:         opUnset,
						Message:           "No active work-session to unset.",
						ActiveWorksession: nil,
					})
				}
				utils.CliInfo("No active work-session to unset.")
				return nil
			}
			if err := RunUnset(); err != nil {
				return utils.NewEnvelopeExitError(opUnset, err, "%s", err)
			}
			if utils.OutputFormat == utils.OutputFormatJSON {
				return printWorkSessionMutationJSON(workSessionMutationOutput{
					OK:                true,
					Operation:         opUnset,
					Message:           "Active work-session cleared.",
					ActiveWorksession: nil,
				})
			}
			utils.CliSuccess("Active work-session cleared.")
			return nil
		}

		if len(args) != 1 {
			return utils.NewUsageEnvelopeExitError(opUse, "SESSION_ID argument is required (or pass --unset)")
		}
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewEnvelopeExitError(opUse, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}
		ws, err := RunUseSession(ac, args[0])
		if errors.Is(err, ErrClusterSessionNotAttachable) {
			return utils.NewUsageEnvelopeExitError(opUse, "%s", err)
		}
		if err != nil {
			return utils.NewEnvelopeExitError(opUse, err, "%s", err)
		}
		message := activeWorkSessionSetMessage("", ws.ID, ws.Description)
		if utils.OutputFormat == utils.OutputFormatJSON {
			active := ws.ID
			return printWorkSessionMutationJSON(newWorkSessionMutationOutput(opUse, message, ws, &active))
		}
		utils.CliSuccess("%s", message)
		return nil
	}),
}

// ErrClusterSessionNotAttachable refuses to set a cluster session as the
// active one.
var ErrClusterSessionNotAttachable = errors.New("a cluster session cannot be the active work-session")

// RunUse validates the work-session via the server, then stores it in config.
// Returns the human-readable description on success.
func RunUse(ac *client.AlpaconClient, uuid string) (string, error) {
	ws, err := RunUseSession(ac, uuid)
	if err != nil {
		return "", err
	}
	return ws.Description, nil
}

// RunUseSession validates the work-session via the server, then stores it in config.
func RunUseSession(ac *client.AlpaconClient, uuid string) (*wsapi.WorkSession, error) {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return nil, err
	}
	if ws.Status != wsapi.StatusActive {
		return nil, fmt.Errorf("work-session %s is in '%s' state and cannot be used", ws.ID, ws.Status)
	}
	// Agent sessions run non-interactively through their own token; attaching one would let
	// interactive exec or websh run against it and fail opaquely. Mirrors the create --use guard.
	if ws.RequesterType == "agent" {
		return nil, fmt.Errorf("work-session %s is an agent session and is not workspace-attachable; agent sessions run non-interactively via their assigned token", ws.ID)
	}
	// The active slot holds the session exec, websh, cp and tunnel run under,
	// and a cluster session serves none of them.
	if len(ws.Clusters) > 0 {
		return nil, fmt.Errorf("%w: work-session %s targets cluster %s; run 'alpacon kube login %s --work-session %s' instead",
			ErrClusterSessionNotAttachable, ws.ID, ws.Clusters[0].Name, ws.Clusters[0].Name, ws.ID)
	}
	// Store the server's canonical ID rather than the raw argument, matching the printed JSON.
	if err := config.SetActiveWorkSessionFor(ac.WorkspaceName, ws.ID); err != nil {
		return nil, err
	}
	return ws, nil
}

// RunUnset clears the active work-session for the current workspace; it is a no-op when nothing
// is set.
func RunUnset() error {
	return config.SetActiveWorkSession("")
}

func init() {
	workSessionUseCmd.Flags().BoolVar(&unsetActiveWorkSession, "unset", false, "Clear the active work-session for the current workspace")
}
