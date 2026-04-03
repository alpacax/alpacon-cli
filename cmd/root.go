package cmd

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/cmd/agent"
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
	"github.com/alpacax/alpacon-cli/cmd/workspace"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:     "alpacon",
	Aliases: []string{"ac"},
	Short: "Access infrastructure securely with Alpacon",
	Long: `Alpacon CLI provides secure access to remote servers without SSH keys, VPNs,
or bastion hosts. Open terminals, execute commands, transfer files, create TCP
tunnels, and manage certificates — all with zero-trust authentication, MFA,
session recording, and role-based access controls.

Designed to be used by engineers, AI coding agents, and CI/CD platforms alike.`,
	Run: func(cmd *cobra.Command, args []string) {
		utils.ShowLogo()
		fmt.Fprintln(os.Stderr, "Welcome to Alpacon CLI! Use 'alpacon [command]' to execute a specific command or 'alpacon help' to see all available commands.")
	},
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		utils.CliErrorWithExit("While executing the command: %s", err)
	}
}

func init() {
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

	// whoami
	RootCmd.AddCommand(whoamiCmd)
}
