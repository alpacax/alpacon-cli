package worksession

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/config"
)

// Resolve returns the work-session UUID to use for an operation.
// Precedence: flag > config. Returns "" when nothing is set.
func Resolve(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return config.GetActiveWorkSession()
}

// AnnounceIfActive prints "Using work-session <uuid>" to stderr when uuid != "".
// Stderr keeps stdout clean for --output json consumers and shell pipelines.
func AnnounceIfActive(uuid string) {
	if uuid == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "Using work-session %s\n", uuid)
}
