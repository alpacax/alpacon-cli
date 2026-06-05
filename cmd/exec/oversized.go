package exec

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// inlineCommandLimit matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

// windowsPlatform is the platform string the server reports for Windows hosts.
const windowsPlatform = "windows"

// exceedsInlineLimit reports whether the command must be sent with the oversized
// flag so the server stages it as a temp script instead of running it inline.
// Byte-based: may flag before the server's char limit, never after.
func exceedsInlineLimit(command string) bool {
	return len(command) > inlineCommandLimit
}

// ResolveOversized reports whether command must be sent with the oversized flag,
// failing fast on Windows servers (no sh wrapper); the server enforces this too.
// Exits on platform lookup failure or unsupported platform. Shared by exec and websh.
func ResolveOversized(ac *client.AlpaconClient, serverName, command string) bool {
	if !exceedsInlineLimit(command) {
		return false
	}
	platform, err := server.GetServerPlatform(ac, serverName)
	if err != nil {
		utils.CliErrorWithExit("Failed to look up server platform: %s", err)
	}
	if platform == windowsPlatform {
		utils.CliErrorWithExit("Oversized commands are not supported on Windows servers.")
	}
	return true
}
