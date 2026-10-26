package kube

import (
	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// exitNotEnabled ends the command with exit 1. The refusal is expected, not a
// CLI fault, so it leaves out the "report an issue" footer CliErrorWithExit adds.
func exitNotEnabled() {
	utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, kubernetes.SurfaceNotEnabledMessage)
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
//
// kube credentials stays reachable under the stand-in. kubectl runs it for a
// context an earlier login wrote, perhaps in another workspace or before a
// logout, and it reads only the saved session, so the current workspace's
// answer says nothing about it.
func ApplySurfaceGate(root *cobra.Command, enabled bool) {
	applySurfaceGate(root, KubeCmd, enabled, kubeCredentialsCmd)
}

// applySurfaceGate moves keep, children of real, under the stand-in.
func applySurfaceGate(root, real *cobra.Command, enabled bool, keep ...*cobra.Command) {
	if enabled {
		return
	}
	root.RemoveCommand(real)
	disabled := newDisabledKubeCmd(real)
	for _, child := range keep {
		real.RemoveCommand(child)
		disabled.AddCommand(child)
	}
	root.AddCommand(disabled)
}

// newDisabledKubeCmd answers every path under real's name and aliases with the
// not-enabled message: no flag parsing and arbitrary args, so nothing Cobra
// checks first can fail.
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
