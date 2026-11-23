package worksession

import (
	"os"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
)

// WorkSessionEnvVar is the environment variable consulted when no --work-session flag is given.
const WorkSessionEnvVar = "ALPACON_WORK_SESSION"

// Resolve returns the effective work-session UUID using flag > env var > config priority.
// The config fallback reads ac's pinned workspace, so the UUID matches where the request goes.
func Resolve(ac *client.AlpaconClient, flagValue string) (string, error) {
	id, _, err := resolveFor(ac.WorkspaceName, flagValue)
	return id, err
}

func resolveFrom(flagValue string, readConfig func() (string, error)) (string, Source, error) {
	if flagValue != "" {
		return flagValue, SourceFlag, nil
	}
	if env := os.Getenv(WorkSessionEnvVar); env != "" {
		return env, SourceEnv, nil
	}
	id, err := readConfig()
	if err != nil {
		return "", SourceNone, err
	}
	if id == "" {
		return "", SourceNone, nil
	}
	return id, SourceConfig, nil
}

func resolveFor(workspace, flagValue string) (string, Source, error) {
	return resolveFrom(flagValue, func() (string, error) {
		return config.GetActiveWorkSessionFor(workspace)
	})
}

// sessionResolvableBeforeClient gates usage output that must come before a client exists; its answer is never sent.
func sessionResolvableBeforeClient(flagValue string) bool {
	uuid, _, err := resolveFrom(flagValue, config.GetActiveWorkSession)
	return err == nil && uuid != ""
}
