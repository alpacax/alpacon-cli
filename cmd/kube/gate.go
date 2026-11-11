package kube

import (
	"slices"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var gatePassThrough = []string{"credentials", "logout"}

// notEnabledError is exit 1 without NewExitError's "report an issue" footer: the refusal is
// expected, not a CLI fault.
func notEnabledError() error {
	return utils.NewExitErrorWithCode(utils.ExitCodeGeneralError, kubernetes.SurfaceNotEnabledMessage)
}

// ApplySurfaceGate replaces KubeCmd under root with a hidden stand-in when the workspace has not
// reported the Kubernetes surface. It must run before root.Execute: Cobra answers --help and
// validates arguments before any PreRun hook, and decides suggestions and completion from Hidden
// alone. Swapping the command leaves the enabled tree exactly as built.
//
// This is a courtesy, not a control: the server refuses /api/kubernetes/ on its own whenever the
// surface is off.
//
// kube credentials stays reachable under the stand-in. kubectl runs it for a context an earlier login
// wrote, perhaps in another workspace or before a logout, and it reads only the saved session, so the
// current workspace's answer says nothing about it. kube logout stays for the same reason: it cleans up
// such a context and its saved token, and needs the server only for a best-effort close.
func ApplySurfaceGate(root *cobra.Command, enabled bool) {
	applySurfaceGate(root, KubeCmd, enabled, gatePassThrough...)
}

// applySurfaceGate moves the children of real named keep under the stand-in.
func applySurfaceGate(root, real *cobra.Command, enabled bool, keep ...string) {
	if enabled {
		return
	}
	// The children kept under the stand-in would inherit its help, which only
	// refuses; they keep the help they had.
	help := root.HelpFunc()
	root.RemoveCommand(real)
	disabled := newDisabledKubeCmd(real)
	for _, child := range real.Commands() {
		if !slices.Contains(keep, child.Name()) {
			continue
		}
		real.RemoveCommand(child)
		child.SetHelpFunc(help)
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
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return notEnabledError()
		}),
	}
	// 'alpacon help kube' reaches the command through Find and calls its help,
	// which has no error to return, so it exits in place.
	disabled.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, kubernetes.SurfaceNotEnabledMessage)
	})
	return disabled
}
