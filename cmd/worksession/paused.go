package worksession

import (
	"fmt"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/utils"
)

// pausedSessionError signals a paused session and returns its refusal; nil for
// any other status. Only a person can resume or end it, and the CLI cannot.
// It lives apart from the config writes of use, which the signal path never reads.
func pausedSessionError(ac *client.AlpaconClient, ws *wsapi.WorkSession) error {
	if ws.Status != wsapi.StatusPaused {
		return nil
	}
	utils.EmitLocalSignal(localsignal.Paused, localsignal.Fields{Session: ws.ID, ConsoleURL: ws.ConsoleURL}, ac)
	return fmt.Errorf("work-session %s is paused and cannot be used; a person must resume or end it in the Alpacon web console", ws.ID)
}
