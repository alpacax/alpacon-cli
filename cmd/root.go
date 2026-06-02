package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/alpacax/alpacon-cli/cmd/agent"
	"github.com/alpacax/alpacon-cli/cmd/approval"
	"github.com/alpacax/alpacon-cli/cmd/audit"
	"github.com/alpacax/alpacon-cli/cmd/authority"
	"github.com/alpacax/alpacon-cli/cmd/cert"
	"github.com/alpacax/alpacon-cli/cmd/csr"
	"github.com/alpacax/alpacon-cli/cmd/event"
	"github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/ftp"
	"github.com/alpacax/alpacon-cli/cmd/iam"
	"github.com/alpacax/alpacon-cli/cmd/log"
	"github.com/alpacax/alpacon-cli/cmd/note"
	"github.com/alpacax/alpacon-cli/cmd/packages"
	"github.com/alpacax/alpacon-cli/cmd/revoke"
	"github.com/alpacax/alpacon-cli/cmd/server"
	"github.com/alpacax/alpacon-cli/cmd/token"
	"github.com/alpacax/alpacon-cli/cmd/tunnel"
	"github.com/alpacax/alpacon-cli/cmd/webftp"
	"github.com/alpacax/alpacon-cli/cmd/webhook"
	"github.com/alpacax/alpacon-cli/cmd/websh"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/cmd/workspace"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:     "alpacon",
	Aliases: []string{"ac"},
	Short:   "Command-line client for Alpacon, the AI-native PAM",
	Long: `Alpacon CLI is the command-line client for Alpacon, the AI-native PAM.
With Alpacon, humans, AI agents, and CI/CD pipelines reach and operate
your entire fleet through a single identity—and every command they run
is judged at runtime, recorded, and bounded by a scoped work session.
If a credential leaks or an AI client is compromised, the damage is
bounded by the session, not by what the credential could touch.

Quick start (interactive auth):

  1. alpacon                                # check current login + workspace
                                            # (run 'alpacon login' or
                                            #  'alpacon workspace switch' if
                                            #  not logged in / wrong place)
  2. alpacon whoami                         # confirm identity + work
                                            # session requirement
  3. alpacon work-session create \          # create + activate a session
       --purpose "describe the task" \
       --scope command,websh \
       --server <server> \
       --use --wait
  4. alpacon exec, websh, cp, tunnel        # operate within the session

Token auth (CI/CD, API automation):

  alpacon login -t <token>                  # work session not required
  alpacon exec ...

See 'alpacon work-session --help' for session lifecycle, gating, and
common error codes.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		switch utils.OutputFormat {
		case utils.OutputFormatTable, utils.OutputFormatJSON:
			return nil
		default:
			return fmt.Errorf("invalid --output value %q (expected %q or %q)",
				utils.OutputFormat, utils.OutputFormatTable, utils.OutputFormatJSON)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		utils.ShowLogo(buildWelcomeLines())
	},
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		utils.CliErrorWithExit("While executing the command: %s", err)
	}
}

func init() {
	// Global output format flag
	RootCmd.PersistentFlags().StringVar(
		&utils.OutputFormat, "output", utils.OutputFormatTable,
		"Output format: table or json",
	)

	// version
	RootCmd.AddCommand(versionCmd)

	// login
	RootCmd.AddCommand(loginCmd)

	// logout
	RootCmd.AddCommand(logoutCmd)

	// iam
	RootCmd.AddCommand(iam.UserCmd)
	RootCmd.AddCommand(iam.GroupCmd)

	// server
	RootCmd.AddCommand(server.ServerCmd)

	// agent
	RootCmd.AddCommand(agent.AgentCmd)

	// websh
	RootCmd.AddCommand(websh.WebshCmd)

	// exec
	RootCmd.AddCommand(exec.ExecCmd)

	// ftp
	RootCmd.AddCommand(ftp.CpCmd)

	// packages
	RootCmd.AddCommand(packages.PackagesCmd)

	// log
	RootCmd.AddCommand(log.LogCmd)

	// event
	RootCmd.AddCommand(event.EventCmd)

	// note
	RootCmd.AddCommand(note.NoteCmd)

	// authority
	RootCmd.AddCommand(authority.AuthorityCmd)

	// csr
	RootCmd.AddCommand(csr.CsrCmd)

	// certificate
	RootCmd.AddCommand(cert.CertCmd)

	// token
	RootCmd.AddCommand(token.TokenCmd)

	// tunnel
	RootCmd.AddCommand(tunnel.TunnelCmd)

	// workspace
	RootCmd.AddCommand(workspace.WorkspaceCmd)

	// revoke
	RootCmd.AddCommand(revoke.RevokeCmd)

	// audit
	RootCmd.AddCommand(audit.AuditCmd)

	// webhook
	RootCmd.AddCommand(webhook.WebhookCmd)

	// webftp
	RootCmd.AddCommand(webftp.WebFTPCmd)

	// work-session
	RootCmd.AddCommand(worksession.WorkSessionCmd)

	// approval
	RootCmd.AddCommand(approval.ApprovalCmd)

	// whoami
	RootCmd.AddCommand(whoamiCmd)
}

// buildWelcomeLines composes the right-side text lines rendered next to the
// Pacabot art when `alpacon` is invoked with no subcommand. Three lines:
// version, workspace URL (or login prompt / config error), help hint.
func buildWelcomeLines() []string {
	header := fmt.Sprintf("alpacon %s", utils.GetCLIVersion())
	helpHint := "'alpacon --help' for commands"

	cfg, err := config.LoadConfig()
	if err != nil {
		// Missing config file → expected case, treat as not logged in.
		// Other errors (permissions, malformed JSON) surface so the user
		// can act on them instead of seeing a misleading login prompt.
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
