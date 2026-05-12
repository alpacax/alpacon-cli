package worksession

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

// WorkSessionEnvVar is the environment variable consulted as a fallback when
// no --work-session flag is given. Resolution order: flag > env var > config.
const WorkSessionEnvVar = "ALPACON_WORK_SESSION"

// Resolve returns the work-session UUID, preferring the flag, then the
// ALPACON_WORK_SESSION env var, then the workspace's active work-session.
func Resolve(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv(WorkSessionEnvVar); env != "" {
		return env, nil
	}
	return config.GetActiveWorkSession()
}

// AnnounceIfActive prints "Using work-session <uuid>" to stderr when uuid is non-empty.
// Stderr keeps stdout clean for --output json.
func AnnounceIfActive(uuid string) {
	if uuid == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "Using work-session %s\n", uuid)
}

// ResolveAndAnnounce resolves the work-session UUID, announces it, and exits on error.
func ResolveAndAnnounce(flagValue string) string {
	uuid, err := Resolve(flagValue)
	if err != nil {
		utils.CliErrorWithExit("%s", err)
	}
	AnnounceIfActive(uuid)
	return uuid
}
