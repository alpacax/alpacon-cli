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

// ResolveOrExit resolves the work-session UUID and exits on error.
func ResolveOrExit(flagValue string) string {
	uuid, err := Resolve(flagValue)
	if err != nil {
		utils.CliErrorWithExit("%s", err)
	}
	return uuid
}
