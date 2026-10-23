package worksession

import (
	"os"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

// WorkSessionEnvVar is the environment variable consulted as a fallback when
// no --work-session flag is given. Resolution order: flag > env var > config.
const WorkSessionEnvVar = "ALPACON_WORK_SESSION"

// Resolve returns the effective work-session UUID using flag > env var > config priority.
func Resolve(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv(WorkSessionEnvVar); env != "" {
		return env, nil
	}
	return config.GetActiveWorkSession()
}

// ResolveFor is Resolve for a caller holding a client: the active slot it
// falls back to is workspaceName's, the workspace the client was built for,
// not whichever one the config file names by now.
func ResolveFor(flagValue, workspaceName string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv(WorkSessionEnvVar); env != "" {
		return env, nil
	}
	return config.GetActiveWorkSessionFor(workspaceName)
}

// ResolveOrExit resolves the work-session UUID and exits on error.
func ResolveOrExit(flagValue string) string {
	uuid, err := Resolve(flagValue)
	if err != nil {
		utils.CliErrorWithExit("%s", err)
	}
	return uuid
}
