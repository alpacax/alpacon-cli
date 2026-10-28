package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"

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

var showVersion bool

var RootCmd = &cobra.Command{
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
		return utils.SetOutputFormat(utils.OutputFormat)
	},
	Run: func(cmd *cobra.Command, args []string) {
		if showVersion {
			runVersion()
			return
		}
		utils.ShowLogo(buildWelcomeLines())
	},
}

func Execute() {
	kube.ApplySurfaceGate(RootCmd, kubernetesSurfaceEnabled())
	RootCmd.InitDefaultCompletionCmd()
	markHookErrors(RootCmd)
	cmd, err := RootCmd.ExecuteC()
	switch {
	case err == nil:
		return
	case !errors.As(err, new(hookError)):
		utils.CliParseErrorWithExit(cmd, utils.OutputFormatIn(os.Args[1:]), err.Error())
	case utils.IsUsageError(err):
		utils.CliUsageErrorWithExit("%s", err)
	default:
		utils.CliErrorWithExit("While executing the command: %s", err)
	}
}

// hookError marks an error a command's own hook returned; any other error is Cobra rejecting the command line.
type hookError struct{ err error }

func (e hookError) Error() string { return e.err.Error() }
func (e hookError) Unwrap() error { return e.err }

// markHookErrors wraps what every error-returning hook of c and its subcommands returns.
func markHookErrors(c *cobra.Command) {
	for _, hook := range []*func(*cobra.Command, []string) error{
		&c.PersistentPreRunE, &c.PreRunE, &c.RunE, &c.PostRunE, &c.PersistentPostRunE,
	} {
		if run := *hook; run != nil {
			*hook = func(cmd *cobra.Command, args []string) error { return markHookError(run(cmd, args)) }
		}
	}
	for _, sub := range c.Commands() {
		markHookErrors(sub)
	}
}

func markHookError(err error) error {
	if err == nil {
		return nil
	}
	return hookError{err: err}
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

func init() {
	RootCmd.PersistentFlags().StringVar(
		&utils.OutputFormat, "output", utils.OutputFormatTable,
		"Output format: table or json",
	)

	RootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, versionCmd.Short)
	RootCmd.AddCommand(versionCmd)

	RootCmd.AddCommand(updateCmd)

	RootCmd.AddCommand(loginCmd)

	RootCmd.AddCommand(logoutCmd)

	RootCmd.AddCommand(iam.UserCmd)
	RootCmd.AddCommand(iam.GroupCmd)

	RootCmd.AddCommand(server.ServerCmd)

	RootCmd.AddCommand(agent.AgentCmd)

	RootCmd.AddCommand(apicmd.ApiCmd)

	RootCmd.AddCommand(websh.WebshCmd)

	RootCmd.AddCommand(exec.ExecCmd)

	RootCmd.AddCommand(ftp.CpCmd)

	RootCmd.AddCommand(edit.EditCmd)

	RootCmd.AddCommand(packages.PackagesCmd)

	RootCmd.AddCommand(log.LogCmd)

	RootCmd.AddCommand(event.EventCmd)

	RootCmd.AddCommand(note.NoteCmd)

	RootCmd.AddCommand(authority.AuthorityCmd)

	RootCmd.AddCommand(csr.CsrCmd)

	RootCmd.AddCommand(cert.CertCmd)

	RootCmd.AddCommand(token.TokenCmd)

	RootCmd.AddCommand(tunnel.TunnelCmd)

	RootCmd.AddCommand(username.UsernameCmd)

	RootCmd.AddCommand(workspace.WorkspaceCmd)

	RootCmd.AddCommand(revoke.RevokeCmd)

	RootCmd.AddCommand(audit.AuditCmd)

	RootCmd.AddCommand(webhook.WebhookCmd)

	RootCmd.AddCommand(webftp.WebFTPCmd)

	RootCmd.AddCommand(worksession.WorkSessionCmd)

	RootCmd.AddCommand(approval.ApprovalCmd)

	RootCmd.AddCommand(kube.KubeCmd)

	RootCmd.AddCommand(acl.AclCmd)

	RootCmd.AddCommand(whoamiCmd)
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
