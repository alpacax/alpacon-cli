package kube

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// NotEnabledMessage is what every 'alpacon kube' path prints when the
// workspace does not expose the Kubernetes surface.
const NotEnabledMessage = "Kubernetes support is not enabled on this workspace. If it was enabled recently, run 'alpacon login' again to refresh."

// exitNotEnabled ends the command with exit 1. The refusal is expected, not a
// CLI fault, so it leaves out the "report an issue" footer CliErrorWithExit adds.
func exitNotEnabled() {
	utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, NotEnabledMessage)
}

// ApplySurfaceGate replaces KubeCmd under root with a hidden stand-in when the
// workspace has not reported the Kubernetes surface. It must run before
// root.Execute: Cobra answers --help and validates arguments before any
// PreRun hook, and decides suggestions and completion from Hidden alone, so
// no hook on the real command could cover every way of reaching it. Swapping
// the command leaves the enabled tree exactly as built.
//
// This is a courtesy, not a control: the server refuses /api/kubernetes/ on
// its own whenever the surface is off.
func ApplySurfaceGate(root *cobra.Command, enabled bool) {
	applySurfaceGate(root, KubeCmd, enabled)
}

func applySurfaceGate(root, real *cobra.Command, enabled bool) {
	if enabled {
		return
	}
	root.RemoveCommand(real)
	root.AddCommand(newDisabledKubeCmd(real))
}

// newDisabledKubeCmd answers every invocation under real's name and aliases
// with NotEnabledMessage. It has no children, so 'kube ls' stops here;
// DisableFlagParsing keeps --help and unknown flags from being handled before
// Run; ArbitraryArgs keeps a missing or extra argument from failing first.
func newDisabledKubeCmd(real *cobra.Command) *cobra.Command {
	disabled := &cobra.Command{
		Use:                real.Use,
		Aliases:            real.Aliases,
		Short:              real.Short,
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		ValidArgsFunction:  cobra.NoFileCompletions,
		Run: func(cmd *cobra.Command, args []string) {
			exitNotEnabled()
		},
	}
	// 'alpacon help kube' reaches the command through Find and calls its help.
	disabled.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		exitNotEnabled()
	})
	return disabled
}
