package worksession

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/utils"
	"golang.org/x/term"
)

const noWorkSessionPromptEnv = "ALPACON_NO_WORKSESSION_PROMPT"

type offerEnv struct {
	stdinTTY     bool
	stderrTTY    bool
	agent        bool
	json         bool
	browserLogin bool
	optOut       bool
}

func readOfferEnv(ios *utils.IOStreams, ac *client.AlpaconClient) offerEnv {
	return offerEnv{
		stdinTTY:     term.IsTerminal(int(os.Stdin.Fd())),
		stderrTTY:    term.IsTerminal(int(os.Stderr.Fd())),
		agent:        aiagent.FromEnvironment().Detected(),
		json:         ios.IsJSON(),
		browserLogin: ac.IsBearerAuth(),
		optOut:       os.Getenv(noWorkSessionPromptEnv) != "",
	}
}

func offerAllowed(env offerEnv) bool {
	return env.stdinTTY && env.stderrTTY && !env.agent && !env.json && env.browserLogin && !env.optOut
}

func decide(code string) bool {
	switch code {
	case utils.WorkSessionRequired, utils.WorkSessionNotUsable, utils.WorkSessionScopeNotAllowed,
		utils.WorkSessionServerNotAllowed, utils.WorkSessionExpired:
		return true
	default:
		return false
	}
}

func (s *Selection) handleRefusal(serverName string, refusal error) (bool, error) {
	if s.need.Server == "" {
		s.need.Server = serverName
	}
	if s.offered || s.need.Server == "" || !decide(utils.StructuredErrorCode(refusal)) {
		return false, nil
	}
	s.offered = true
	s.ios.Warning("The server refused the request: %s.", utils.WorkSessionReason(utils.StructuredErrorCode(refusal)))

	sessions, want, err := s.reusableSessions()
	if err != nil {
		s.ios.Warning("Could not look up your WorkSessions: %s", err)
		return false, nil
	}
	if candidate, wait := pick(sessions, want, time.Now(), utils.DefaultApprovalWaitTimeout); candidate != nil {
		if utils.PromptForBool(reusePrompt(candidate, wait, s.need.Account)) {
			return s.settle(candidate, wait, want, "")
		}
	}

	if !utils.PromptForBool(fmt.Sprintf("Create a WorkSession for %s with scopes %s now?", utils.SanitizeTerminalText(s.need.Server), strings.Join(s.need.scopes(), ", "))) {
		return false, nil
	}
	purpose, err := utils.ReadRequiredInput("Purpose (what you're doing and why): ")
	if err != nil {
		return false, nil
	}
	var parsed time.Duration
	for {
		duration, readErr := utils.ReadInputWithDefault("Duration [1h]: ", "1h")
		if readErr != nil {
			return false, nil
		}
		parsed, err = utils.ParsePositiveDuration("duration", duration)
		if err == nil {
			break
		}
		s.ios.Warning("Could not use that duration: %s", err)
	}
	created, err := wsapi.CreateWorkSession(s.ac, wsapi.WorkSessionCreateRequest{
		Description:           purpose,
		RequesterType:         "user",
		Scopes:                s.need.scopes(),
		Servers:               []string{want.ServerID},
		Duration:              wsapi.FormatDuration(parsed),
		TargetAccountUsername: s.need.Account,
	})
	if err != nil {
		if hint := TargetErrorHint(err); hint != "" {
			s.ios.Warning("Could not create a WorkSession: %s.", hint)
		} else {
			s.ios.Warning("Could not create a WorkSession: %s", err)
		}
		return false, nil
	}
	return s.settle(created, created.Status != wsapi.StatusActive, want, s.need.Account)
}

func (s *Selection) reusableSessions() ([]wsapi.WorkSession, Want, error) {
	me, err := iam.GetCurrentUser(s.ac)
	if err != nil {
		return nil, Want{}, err
	}
	if strings.TrimSpace(me.ID) == "" {
		return nil, Want{}, errors.New("the current user has no ID")
	}
	serverID, err := server.GetServerIDByName(s.ac, s.need.Server)
	if err != nil {
		return nil, Want{}, err
	}
	sessions, err := wsapi.ListWorkSessions(s.ac, wsapi.SessionFilter{
		Status:        strings.Join([]string{wsapi.StatusActive, wsapi.StatusApproved, wsapi.StatusPending}, ","),
		RequesterType: "user",
		AssignedUser:  me.ID,
	})
	if err != nil {
		return nil, Want{}, err
	}
	want := Want{ServerID: serverID, AssignedUser: me.ID, RequesterType: "user", Scopes: s.need.scopes(), Exclude: s.id}
	return sessions, want, nil
}

func reusePrompt(session *wsapi.WorkSession, wait bool, account string) string {
	id, _ := utils.SanitizeTerminalLine(session.ID)
	status := "active"
	if wait && session.Status == wsapi.StatusApproved {
		status = "approved, not yet started"
	} else if wait {
		status = "pending approval"
	}
	details := []string{fmt.Sprintf("status: %s", status)}
	if purpose, _ := utils.SanitizeTerminalLine(session.Description); strings.TrimSpace(purpose) != "" {
		details = append(details, "purpose: "+purpose)
	}
	if !session.ExpiresAt.IsZero() {
		details = append(details, "expires: "+session.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if account != "" {
		accountLine, _ := utils.SanitizeTerminalLine(account)
		details = append(details, "approved account cannot be checked; requested account: "+accountLine)
	}
	return fmt.Sprintf("Use WorkSession %s (%s)?", id, strings.Join(details, "; "))
}

// settle makes session the one the operation sends. createdFor is the account it was created for,
// empty for a reused session, whose account the server does not report.
func (s *Selection) settle(session *wsapi.WorkSession, wait bool, want Want, createdFor string) (bool, error) {
	if !safeResumeID(session.ID) {
		s.ios.Warning("The offered WorkSession has an unsafe ID and cannot be selected.")
		return false, nil
	}
	id := utils.SanitizeTerminalText(session.ID)
	if wait {
		resumeAction := afterApprovalAction("user", id, createdFor)
		guidance := ""
		if account := accountRunGuidance(createdFor); account != "" {
			guidance = " (" + account + ")"
		}
		s.ios.Info("To stop and come back later, press Ctrl+C; after approval, run '%s', then re-run this command%s.", resumeAction.Command, guidance)
		final, err := WaitForApproval(s.ios, s.ac, session.ID, true, ApprovalPollInterval, utils.DefaultApprovalWaitTimeout)
		if err != nil {
			resumeAction.Description = strings.TrimPrefix(resumeAction.Description, "after approval")
			resumeAction.Description = "set this session active, then re-run the command" + resumeAction.Description
			return false, WaitExitError(s.ios, opCreate, session, resumeAction, err)
		}
		session = final
		PrintSessionAdvisories(s.ios, final)
	} else {
		active, err := wsapi.GetWorkSession(s.ac, session.ID)
		if err != nil {
			s.ios.Warning("Could not verify WorkSession %s: %s", id, err)
			return false, nil
		}
		session = active
	}
	active := session
	if !safeResumeID(active.ID) || !want.Usable(active, time.Now()) {
		s.ios.Warning("WorkSession %s does not grant the required access.", id)
		return false, nil
	}
	err := config.SetActiveWorkSessionFor(s.ac.WorkspaceName, active.ID)
	if err != nil {
		s.ios.Warning("Could not set WorkSession %s active for this workspace: %s", id, err)
		return false, nil
	}
	s.id = active.ID
	s.source = SourceConfig
	success := fmt.Sprintf("WorkSession %s is now the active session for workspace %s", utils.SanitizeTerminalText(active.ID), utils.SanitizeTerminalText(s.ac.WorkspaceName))
	if guidance := accountRunGuidance(createdFor); guidance != "" {
		success += "; " + guidance
	}
	s.ios.Success("%s.", success)
	return true, nil
}

func safeResumeID(id string) bool {
	return id != "" && !strings.ContainsFunc(id, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return false
		default:
			return true
		}
	})
}
