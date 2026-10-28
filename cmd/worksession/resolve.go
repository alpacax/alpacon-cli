package worksession

import (
	"os"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

// WorkSessionEnvVar is the environment variable consulted as a fallback when
// no --work-session flag is given. Resolution order: flag > env var > config.
const WorkSessionEnvVar = "ALPACON_WORK_SESSION"

// Resolve returns the effective work-session UUID using flag > env var > config priority.
// The config fallback reads ac's pinned workspace, so the UUID matches where the request goes.
func Resolve(ac *client.AlpaconClient, flagValue string) (string, error) {
	return resolveFrom(flagValue, func() (string, error) {
		return config.GetActiveWorkSessionFor(ac.WorkspaceName)
	})
}

func resolveFrom(flagValue string, readConfig func() (string, error)) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv(WorkSessionEnvVar); env != "" {
		return env, nil
	}
	return readConfig()
}

// sessionResolvableBeforeClient gates usage output that must come before a client exists; its answer is never sent.
func sessionResolvableBeforeClient(flagValue string) bool {
	uuid, err := resolveFrom(flagValue, config.GetActiveWorkSession)
	return err == nil && uuid != ""
}

// ResolveOrExit resolves the work-session UUID and exits on error.
func ResolveOrExit(ac *client.AlpaconClient, flagValue string) string {
	uuid, err := Resolve(ac, flagValue)
	if err != nil {
		utils.CliErrorWithExit("%s", err)
	}
	return uuid
}
