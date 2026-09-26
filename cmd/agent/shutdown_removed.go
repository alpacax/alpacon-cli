package agent

import (
	"github.com/alpacax/alpacon-cli/cmd/removed"
)

// TODO(remove): drop this stub in the release after v1.14.0 (#477).
var shutdownAgentRemovedCmd = removed.Command("shutdown", "alpacon agent shutdown",
	"The agent can no longer be stopped from the CLI. To restart it instead, run:\n"+
		"  alpacon agent restart <server>")
