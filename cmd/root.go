package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/acl"
	"github.com/alpacax/alpacon-cli/cmd/agent"
	apicmd "github.com/alpacax/alpacon-cli/cmd/api"
	"github.com/alpacax/alpacon-cli/cmd/approval"
	"github.com/alpacax/alpacon-cli/cmd/audit"
	"github.com/alpacax/alpacon-cli/cmd/authority"
	"github.com/alpacax/alpacon-cli/cmd/cert"
	"github.com/alpacax/alpacon-cli/cmd/csr"
	"github.com/alpacax/alpacon-cli/cmd/edit"
	"github.com/alpacax/alpacon-cli/cmd/event"
	"github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/ftp"
	"github.com/alpacax/alpacon-cli/cmd/iam"
	"github.com/alpacax/alpacon-cli/cmd/kube"
	"github.com/alpacax/alpacon-cli/cmd/log"
	"github.com/alpacax/alpacon-cli/cmd/note"
	"github.com/alpacax/alpacon-cli/cmd/packages"
	"github.com/alpacax/alpacon-cli/cmd/revoke"
	"github.com/alpacax/alpacon-cli/cmd/server"
	"github.com/alpacax/alpacon-cli/cmd/token"
	"github.com/alpacax/alpacon-cli/cmd/tunnel"
	"github.com/alpacax/alpacon-cli/cmd/username"
	"github.com/alpacax/alpacon-cli/cmd/webftp"
	"github.com/alpacax/alpacon-cli/cmd/webhook"
	"github.com/alpacax/alpacon-cli/cmd/websh"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/cmd/workspace"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

// newRootCmd builds the whole command tree, handing each command the client factory and the streams it takes.
func newRootCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	var showVersion bool
	var output string
	versionCmd := newVersionCmd(ios)
	root := &cobra.Command{
		Use:           "alpacon",
		Aliases:       []string{"ac"},
		SilenceErrors: true, // Execute reports every error once, in the --output format
		SilenceUsage:  true,
		Short:         "Use Alpacon, the AI-native PAM, from the terminal",
		Long: `Alpacon CLI is the command-line client for Alpacon, the AI-native PAM.
Humans, AI agents, and CI/CD run commands—each judged, recorded, and bounded.

Quick start (for humans and AI agents):

  1. alpacon server ls                      # find the server to target
  2. alpacon work-session create \          # open + activate a scoped session
       --purpose "<what you're doing>" \    #   (prints a SESSION_ID)
       --scope command,websh \              #   (scopes: command, editor, sudo, tunnel, webftp, websh)
       --server <SERVER> \
       --expires-in 1h \                    #   (required non-interactively; or --expires-at <RFC3339>)
       --use --wait
  3. alpacon exec <SERVER> -- <COMMAND>     # run work inside the session
  4. alpacon work-session complete <SESSION_ID>  # finish the session when done

AI agents add --requester-type agent to step 2 and drop --use and websh, which an
agent session refuses; step 3 then names the session before the server:
  alpacon exec --work-session <SESSION_ID> <SERVER> -- <COMMAND>

For a Kubernetes cluster, 'alpacon kube login <CLUSTER>' finds or creates a
session that targets the cluster (scopes kube, kube_exec) and writes the
kubeconfig context kubectl uses.

See 'alpacon work-session --help' for session lifecycle and error codes.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return ios.SetOutputFormat(output)
		},
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			if showVersion {
				return runVersion(ios)
			}
			utils.ShowLogo(buildWelcomeLines())
			return nil
		}),
	}
	// A local, not utils.OutputFormat: binding the global would write its default each time a tree is built.
	root.PersistentFlags().StringVar(&output, "output", utils.OutputFormatTable, "Output format: table or json")
	root.Flags().BoolVarP(&showVersion, "version", "v", false, versionCmd.Short)
	root.AddCommand(
		versionCmd,
		newUpdateCmd(ios),
		newLoginCmd(newClient, ios),
		newLogoutCmd(newClient, ios),
		iam.NewUserCmd(newClient, ios),
		iam.NewGroupCmd(newClient, ios),
		server.NewServerCmd(newClient, ios),
		agent.NewAgentCmd(newClient, ios),
		apicmd.NewApiCmd(newClient),
		websh.NewWebshCmd(newClient, ios),
		exec.NewExecCmd(newClient, ios),
		ftp.NewCpCmd(newClient, ios),
		edit.NewEditCmd(newClient, ios),
		packages.NewPackagesCmd(newClient, ios),
		log.NewLogCmd(newClient, ios),
		event.NewEventCmd(newClient, ios),
		note.NewNoteCmd(newClient, ios),
		authority.NewAuthorityCmd(newClient, ios),
		csr.NewCsrCmd(newClient, ios),
		cert.NewCertCmd(newClient, ios),
		token.NewTokenCmd(newClient, ios),
		tunnel.NewTunnelCmd(newClient, ios),
		username.NewUsernameCmd(newClient, ios),
		workspace.NewWorkspaceCmd(newClient, ios),
		revoke.NewRevokeCmd(newClient, ios),
		audit.NewAuditCmd(newClient, ios),
		webhook.NewWebhookCmd(newClient, ios),
		webftp.NewWebFTPCmd(newClient, ios),
		worksession.NewWorkSessionCmd(newClient, ios),
		approval.NewApprovalCmd(newClient, ios),
		kube.NewKubeCmd(newClient, ios),
		acl.NewAclCmd(ios),
		newWhoamiCmd(newClient, ios),
	)
	return root
}

func Execute() {
	utils.LocalSignalConsoleOrigin = localSignalConsoleOrigin
	root := newRootCmd(client.NewAlpaconAPIClient, utils.Stdio)
	kube.ApplySurfaceGate(root, kubernetesSurfaceEnabled())
	root.InitDefaultCompletionCmd()
	rejectUnknownSubcommands(root)
	utils.MarkHookErrors(root)
	keepWithOwnParser(root, os.Args[1:])
	cmd, err := root.ExecuteC()
	exitErr, isExitErr := errors.AsType[*utils.ExitError](err)
	switch {
	case err == nil:
		utils.WaitForTokenRefreshes()
		return
	case isExitErr: // a converted command's failure, printed after its deferred calls ran
		utils.PrintExitError(exitErr)
		utils.Exit(exitErr.Code)
	case !utils.IsHookError(err):
		utils.CliParseErrorWithExit(cmd, utils.OutputFormatIn(os.Args[1:]), err.Error())
	case utils.IsUsageError(err):
		utils.CliUsageErrorWithExit("%s", err)
	default:
		utils.CliErrorWithExit("While executing the command: %s", err)
	}
}

// keepWithOwnParser detaches the subcommands of a command that parses its own flags when a flag Cobra
// does not know comes before the subcommand's name: Cobra took the server for that flag's value.
func keepWithOwnParser(root *cobra.Command, args []string) {
	cmd, _, err := root.Find(args)
	if err != nil || !cmd.HasParent() || cmd.Parent() == root {
		return
	}
	parent := cmd.Parent()
	if parent.Parent() != root || !parent.DisableFlagParsing {
		return
	}
	at := slices.IndexFunc(args, func(arg string) bool { return arg == parent.Name() || parent.HasAlias(arg) })
	for _, arg := range args[at+1:] {
		if arg == cmd.Name() || cmd.HasAlias(arg) {
			return
		}
		if strings.HasPrefix(arg, "-") && !knownFlag(parent, arg) {
			parent.RemoveCommand(parent.Commands()...)
			return
		}
	}
}

func knownFlag(c *cobra.Command, arg string) bool {
	if name, ok := strings.CutPrefix(arg, "--"); ok {
		name, _, _ = strings.Cut(name, "=")
		return c.Flags().Lookup(name) != nil
	}
	return len(arg) == 2 && c.Flags().ShorthandLookup(arg[1:]) != nil
}

// rejectUnknownSubcommands does below the root what Cobra does only at the root, for each group that leaves Args and flag parsing to Cobra.
func rejectUnknownSubcommands(c *cobra.Command) {
	if c.HasParent() && c.HasSubCommands() && c.Args == nil && !c.DisableFlagParsing {
		c.Args = utils.RejectUnknownSubcommand()
	}
	for _, sub := range c.Commands() {
		rejectUnknownSubcommands(sub)
	}
}

// kubernetesSurfaceEnabled reads the answer the last login or switch recorded;
// no readable config or no credential means nothing can reach the clusters.
func kubernetesSurfaceEnabled() bool {
	cfg, err := config.LoadConfig()
	if err != nil {
		return false
	}
	return (cfg.AccessToken != "" || cfg.Token != "") && cfg.KubernetesSurface
}

// localSignalConsoleOrigin names the console a local signal links to, from the saved login.
func localSignalConsoleOrigin() (string, string) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return "", ""
	}
	return cfg.WorkspaceURL, cfg.BaseDomain
}

// buildWelcomeLines composes the lines shown beside the Pacabot art when alpacon
// runs with no subcommand: version, workspace URL (or login prompt or config
// error), and a help hint.
func buildWelcomeLines() []string {
	header := fmt.Sprintf("alpacon %s", utils.GetCLIVersion())
	helpHint := "'alpacon --help' for commands"

	cfg, err := config.LoadConfig()
	if err != nil {
		// A missing config means not logged in; any other error surfaces so the user can
		// act on it rather than see a misleading login prompt.
		if errors.Is(err, os.ErrNotExist) {
			return []string{header, "Not logged in — run 'alpacon login'", helpHint}
		}
		return []string{header, "Config read error — run 'alpacon login' to reset", helpHint}
	}
	if cfg.AccessToken == "" && cfg.Token == "" {
		return []string{header, "Not logged in — run 'alpacon login'", helpHint}
	}

	host := hostFromURL(cfg.WorkspaceURL)
	if host == "" {
		host = cfg.WorkspaceName
	}
	return []string{header, host, helpHint}
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}
