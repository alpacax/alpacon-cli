package worksession

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

// Resolve returns the work-session UUID, with the flag taking precedence over config.
func Resolve(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return config.GetActiveWorkSession()
}

// AnnounceIfActive prints "Using work-session <uuid>" to stderr when uuid is non-empty.
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
