package worksession

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/server"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const pendingWorkSessionStatus = "pending"

var (
	updateTitle       string
	updateDescription string
	updateScopes      []string
	updateServers     []string
	updateStartsAt    string
	updateExpiresAt   string
	updateSudo        []string
	updateSudoReason  string
)

var workSessionUpdateCmd = &cobra.Command{
	Use:   "update [SESSION_ID]",
	Short: "Update an existing work session",
	Long: `Update fields of an existing work session.

Only the flags you pass are changed; the rest are left untouched. Which fields
the server accepts depends on the session status (e.g. --starts-at/--expires-at
on a pending session; --sudo on an approved/active one). The CLI sends what you
provide and surfaces the server's validation error if a field isn't editable for
the current status (--sudo is also validated locally before the request).

--scope and --server replace the whole list (not append). --sudo adds MFA-bypass
sudo command patterns to the session's existing policies; this is the recovery
path when an 'exec' sudo was denied. The additions may require approval before
they take effect.

If SESSION_ID is omitted, the effective work session is resolved from the
ALPACON_WORK_SESSION environment variable, then the workspace's active session
(set via 'alpacon work-session use').`,
	Args: cobra.MaximumNArgs(1),
	Example: `  alpacon work-session update ses-abc123 --title "deploy v2" --description "rollout"
  alpacon work-session update ses-abc123 --server web-01,db-01
  alpacon work-session update ses-abc123 --scope command,websh
  alpacon work-session update ses-abc123 --starts-at 2027-01-15T10:00:00Z --expires-at 2027-01-15T12:00:00Z
  alpacon work-session update ses-abc123 --sudo "systemctl restart nginx"
  alpacon work-session update --sudo "tail -f /var/log/nginx/*.log"`,
	Run: func(cmd *cobra.Command, args []string) {
		var sessionID string
		if len(args) == 1 {
			sessionID = args[0]
		} else {
			resolved, err := Resolve("")
			if err != nil || resolved == "" {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "No SESSION_ID given and no active work session. Pass an ID, set ALPACON_WORK_SESSION, or run 'alpacon work-session use <id>' first.")
			}
			sessionID = resolved
		}

		var req wsapi.WorkSessionUpdateRequest
		var newSudo []wsapi.SudoPolicyInline
		changed := 0
		if cmd.Flags().Changed("title") {
			title := strings.TrimSpace(updateTitle)
			if title == "" {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "--title cannot be empty.")
			}
			req.Title = title
			changed++
		}
		if cmd.Flags().Changed("description") {
			description := strings.TrimSpace(updateDescription)
			if description == "" {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "--description cannot be empty.")
			}
			req.Description = description
			changed++
		}
		if cmd.Flags().Changed("scope") {
			scopes := utils.CompactStrings(updateScopes)
			if len(scopes) == 0 {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "--scope must contain at least one scope.")
			}
			if err := validateScopeEnum(scopes); err != nil {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "Invalid --scope: %s", err)
			}
			req.Scopes = scopes
			changed++
		}
		if cmd.Flags().Changed("starts-at") {
			val, err := parseRFC3339Flag("--starts-at", updateStartsAt)
			if err != nil {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "%s.", err)
			}
			req.StartsAt = val
			changed++
		}
		if cmd.Flags().Changed("expires-at") {
			val, err := parseRFC3339Flag("--expires-at", updateExpiresAt)
			if err != nil {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "%s.", err)
			}
			req.ExpiresAt = val
			changed++
		}
		var serverNames []string
		if cmd.Flags().Changed("server") {
			serverNames = utils.CompactStrings(updateServers)
			if len(serverNames) == 0 {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "--server must contain at least one valid server name.")
			}
			changed++ // names resolved to IDs below once the client exists
		}
		if cmd.Flags().Changed("sudo") {
			newSudo = buildSudoPolicies(updateSudo, updateSudoReason)
			if len(newSudo) == 0 {
				utils.CliUsageErrorEnvelopeWithExit(opUpdate, "--sudo must contain at least one command pattern.")
			}
			changed++
		} else if cmd.Flags().Changed("sudo-reason") {
			utils.CliUsageErrorEnvelopeWithExit(opUpdate, "--sudo-reason has no effect without --sudo.")
		}

		if changed == 0 {
			utils.CliUsageErrorEnvelopeWithExit(opUpdate, "Nothing to update. Pass at least one of --title, --description, --scope, --server, --starts-at, --expires-at, or --sudo.")
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opUpdate, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if len(serverNames) > 0 {
			ids, err := server.ResolveServerNames(ac, serverNames)
			if err != nil {
				utils.CliErrorEnvelopeWithExit(opUpdate, err, "%s.", err)
			}
			req.Servers = ids
		}

		updated, err := applyWorkSessionUpdate(ac, sessionID, req, newSudo)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opUpdate, err, "%s.", err)
		}

		message := fmt.Sprintf("Work session %s updated (status: %s).", updated.ID, updated.Status)
		if utils.OutputFormat == utils.OutputFormatJSON {
			printWorkSessionMutationJSON(newWorkSessionMutationOutput(opUpdate, message, updated, nil))
			return
		}
		utils.CliSuccess("%s", message)
		if len(newSudo) > 0 {
			utils.CliInfo("Added sudo policies may require approval before they take effect. Re-run your command once the session reflects the change.")
		}
	},
}

// scopes is the effective scope list: req.Scopes when --scope replaces the list, else the session's current scopes.
func validateSessionForSudoUpdate(session *wsapi.WorkSession, scopes []string) error {
	if session.Status == pendingWorkSessionStatus {
		return fmt.Errorf(
			"work session %s is pending approval; sudo policies cannot be modified yet. Set them at create time with --sudo, or wait until it is approved",
			session.ID,
		)
	}
	if !slices.Contains(scopes, "sudo") {
		return fmt.Errorf(
			"work session %s does not include the 'sudo' scope; sudo policies cannot be added. Include 'sudo' in the scope list via --scope in this same update (note --scope replaces the whole list, so list every scope you want to keep), or create a new session with --sudo (which adds the 'sudo' scope automatically)",
			session.ID,
		)
	}
	return nil
}

// sudo_policies is PUT-style: echo existing policies back so adding doesn't delete them.
func applyWorkSessionUpdate(ac *client.AlpaconClient, sessionID string, req wsapi.WorkSessionUpdateRequest, newSudo []wsapi.SudoPolicyInline) (*wsapi.WorkSession, error) {
	if len(newSudo) > 0 {
		session, err := wsapi.GetWorkSession(ac, sessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to load work session %s: %w", sessionID, err)
		}
		effectiveScopes := session.Scopes
		if len(req.Scopes) > 0 {
			effectiveScopes = req.Scopes
		}
		if err := validateSessionForSudoUpdate(session, effectiveScopes); err != nil {
			return nil, err
		}
		desired := make([]wsapi.SudoPolicyInline, 0, len(session.SudoPolicies)+len(newSudo))
		desired = append(desired, session.SudoPolicies...)
		desired = append(desired, newSudo...)
		req.SudoPolicies = desired
	}
	return wsapi.UpdateWorkSession(ac, sessionID, req)
}

func parseRFC3339Flag(flag, val string) (string, error) {
	val = strings.TrimSpace(val)
	if _, err := time.Parse(time.RFC3339, val); err != nil {
		return "", fmt.Errorf("invalid %s value %q: must be RFC3339 format", flag, val)
	}
	return val, nil
}

func init() {
	workSessionUpdateCmd.Flags().StringVar(&updateTitle, "title", "", "New session title")
	workSessionUpdateCmd.Flags().StringVar(&updateDescription, "description", "", "New session description (markdown supported)")
	workSessionUpdateCmd.Flags().StringSliceVar(&updateScopes, "scope", nil, "Replace the session scopes. Valid: command, editor, sudo, tunnel, webftp, websh (repeatable; comma-separated values also accepted; replaces the whole list)")
	workSessionUpdateCmd.Flags().StringSliceVar(&updateServers, "server", nil, "Replace the target servers by name (repeatable; comma-separated values also accepted; replaces the whole list)")
	workSessionUpdateCmd.Flags().StringVar(&updateStartsAt, "starts-at", "", "New scheduled start time (RFC3339; pending sessions only)")
	workSessionUpdateCmd.Flags().StringVar(&updateExpiresAt, "expires-at", "", "New absolute expiry time (RFC3339; pending sessions only — use 'extend' for approved/active sessions)")
	workSessionUpdateCmd.Flags().StringArrayVar(&updateSudo, "sudo", nil, "Sudo command patterns to add as MFA-bypass policies (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed)")
	workSessionUpdateCmd.Flags().StringVar(&updateSudoReason, "sudo-reason", "", "Justification applied to the sudo policies added via --sudo")
}
