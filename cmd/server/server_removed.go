package server

import (
	"github.com/alpacax/alpacon-cli/cmd/removed"
)

// TODO(remove): drop these stubs in the release after v1.14.0 (#477).
var (
	serverRebootRemovedCmd = removed.Command("reboot", "alpacon server reboot",
		"Reboot the host inside a work session instead, for example:\n"+
			"  alpacon exec <server> -- sudo reboot\n"+
			"Mute the server's alerts before a planned reboot.")
	serverShutdownRemovedCmd = removed.Command("shutdown", "alpacon server shutdown",
		"Shut down the host inside a work session instead, for example:\n"+
			"  alpacon exec <server> -- sudo shutdown -h now\n"+
			"Mute the server's alerts before a planned shutdown.")
	serverUpgradeRemovedCmd = removed.Command("upgrade", "alpacon server upgrade",
		"Upgrade the host's packages inside a work session instead, with the distribution's package manager, for example:\n"+
			"  alpacon exec <server> -- sudo apt-get upgrade -y\n"+
			"Mute the server's alerts before a planned reboot.")
)
