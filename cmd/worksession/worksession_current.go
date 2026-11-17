package worksession

import (
	"encoding/json"
	"errors"
	"fmt"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionCurrentCmd = newWorkSessionCurrentCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newWorkSessionCurrentCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:     "current",
		Short:   "Show the active work-session in this workspace",
		Example: `  alpacon work-session current`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			// Checked before any client exists so an unset 'current' neither logs in nor refreshes a token.
			if uuid, err := config.GetActiveWorkSession(); err != nil {
				return utils.NewEnvelopeExitError(opCurrent, err, "%s", err)
			} else if uuid == "" {
				printNoActiveSession(ios)
				return nil
			}
			ac, err := newClient()
			if err != nil {
				err = fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
				return utils.NewEnvelopeExitError(opCurrent, err, "%s", err)
			}
			if err := printCurrent(ios, ac); err != nil {
				if exitErr, ok := errors.AsType[*utils.ExitError](err); ok { // PrintJson's failure names no operation
					return exitErr
				}
				return utils.NewEnvelopeExitError(opCurrent, err, "%s", err)
			}
			return nil
		}),
	}
}

// RunCurrent returns the active work-session UUID and its server-side details.
// Returns ("", nil, nil) when nothing is set, or (uuid, nil, err) if set but unreachable.
func RunCurrent(ac *client.AlpaconClient) (string, *wsapi.WorkSession, error) {
	uuid, err := config.GetActiveWorkSessionFor(ac.WorkspaceName)
	if err != nil {
		return "", nil, err
	}
	if uuid == "" {
		return "", nil, nil
	}
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return uuid, nil, err
	}
	return uuid, ws, nil
}

// printCurrent re-reads under ac's pinned workspace: another shell may have switched since the first check.
func printCurrent(ios *utils.IOStreams, ac *client.AlpaconClient) error {
	uuid, err := config.GetActiveWorkSessionFor(ac.WorkspaceName)
	if err != nil {
		return err
	}
	if uuid == "" {
		printNoActiveSession(ios)
		return nil
	}
	if ios.IsJSON() {
		return WriteCurrentRaw(ios, ac, uuid)
	}
	return WriteCurrentTable(ios, ac, uuid)
}

func printNoActiveSession(ios *utils.IOStreams) {
	if ios.IsJSON() {
		_, _ = fmt.Fprintln(ios.Out, "null")
		return
	}
	ios.Info("No active work-session.")
}

func WriteCurrentRaw(ios *utils.IOStreams, ac *client.AlpaconClient, uuid string) error {
	body, err := wsapi.GetWorkSessionRaw(ac, uuid)
	if err != nil {
		return staleActiveSessionError(uuid, err)
	}
	var session struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &session) == nil {
		warnIfEnded(ios, uuid, session.Status)
	}
	return ios.PrintJson(body)
}

// WriteCurrentTable projects the session through ProjectAttributes so the columns match
// 'work-session ls', and marks it as 'ls' does.
func WriteCurrentTable(ios *utils.IOStreams, ac *client.AlpaconClient, uuid string) error {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return staleActiveSessionError(uuid, err)
	}
	rows := []wsapi.WorkSessionAttributes{wsapi.ProjectAttributes(ws)}
	MarkActive(rows, uuid)
	warnIfEnded(ios, uuid, ws.Status)
	return ios.PrintTable(rows)
}

func warnIfEnded(ios *utils.IOStreams, uuid, status string) {
	if wsapi.IsEndedStatus(status) {
		ios.Warning("Active work-session %s has ended (%s). Run 'alpacon work-session use --unset' to clear it.", uuid, status)
	}
}

func staleActiveSessionError(uuid string, cause error) error {
	return fmt.Errorf("active work-session %s no longer accessible: %w (run 'alpacon work-session use --unset' to clear)", uuid, cause)
}
