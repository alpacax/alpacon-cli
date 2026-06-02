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
	"github.com/alpacax/alpacon-cli/cmd/edit"
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
Humans, AI agents, and CI/CD run commands—each judged, recorded, and bounded.

Quick start (for humans and AI agents):

  1. alpacon server ls                      # find the server to target
  2. alpacon work-session create \          # open + activate a scoped session
       --purpose "<what you're doing>" \    #   (prints a SESSION_ID)
       --scope command,websh \              #   (scopes: command, editor, sudo, tunnel, webftp, websh)
       --server <server> \
       --use --wait
  3. alpacon exec <server> -- <command>     # run work inside the session
  4. alpacon work-session complete <id>     # finish the session when done

See 'alpacon work-session --help' for session lifecycle and error codes.`,
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

	// edit
	RootCmd.AddCommand(edit.EditCmd)

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
