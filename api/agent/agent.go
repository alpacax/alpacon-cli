package agent

import (
	"fmt"
	"path"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	baseURL        = "/api/servers/servers/"
	upgradeAction  = "upgrade_agent"
	restartAction  = "restart_agent"
	shutdownAction = "shutdown_agent"
)

var actionMap = map[string]string{
	"upgrade":  upgradeAction,
	"restart":  restartAction,
	"shutdown": shutdownAction,
}

func RequestAgentAction(ac *client.AlpaconClient, serverName string, action string) error {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	actionValue, ok := actionMap[action]
	if !ok {
		return fmt.Errorf("invalid action: %s. Valid actions are: upgrade, restart, shutdown", action)
	}

	request := RequestAgent{
		Action: actionValue,
	}

	relativePath := path.Join(serverID, "actions")
	_, err = ac.SendPostRequest(utils.BuildURL(baseURL, relativePath, nil), request)
	return err
}
