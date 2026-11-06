package utils

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/alpacax/alpacon-cli/pkg/aiagent"
)

// Operations HandleWorkSessionError reports; each is also the WorkSession scope it requires.
const (
	OperationCommand = "command"
	OperationWebFTP  = "webftp"
	OperationWebsh   = "websh"
	// OperationKube targets a cluster rather than a server: it needs a session
	// that holds the kube scope and targets that cluster.
	OperationKube = "kube"
)

// agentForbiddenScopes are the scopes an agent session cannot hold: an
// interactive terminal, and exec into a pod.
var agentForbiddenScopes = []string{OperationWebsh, "kube_exec"}

// IsAgentForbiddenScope reports whether an agent session cannot hold scope.
func IsAgentForbiddenScope(scope string) bool {
	return slices.Contains(agentForbiddenScopes, scope)
}

// AgentSessionScope is the scope an agent session requests for operation. An
// agent cannot hold websh, so its work on the server runs through exec under
// the command scope instead.
func AgentSessionScope(operation string) string {
	if IsAgentForbiddenScope(operation) {
		return OperationCommand
	}
	return operation
}

type workSessionErrorJSON = JSONErrorEnvelope[workSessionErrorCtx]

// accountSessionCreateDescription is the account-not-approved refusal's
// pointer to a new session; the server's hint for that refusal already says
// to request a work session for the account, so workSessionGuidance drops it
// when a hint is there.
const accountSessionCreateDescription = "or create a session for this account"

type workSessionErrorCtx struct {
	AuthMethod         string   `json:"auth_method"`
	RequiredScope      string   `json:"required_scope"`
	TargetServers      []string `json:"target_servers"`
	CurrentWorksession *string  `json:"current_worksession"`
	// TargetClusters is set only for kube, whose target is a cluster; kube
	// keeps TargetServers an empty array so the field's type stays stable.
	TargetClusters []string `json:"target_clusters,omitempty"`
}

var workSessionReasonMap = map[string]string{
	WorkSessionRequired:             "no WorkSession selected for this shell",
	WorkSessionNotUsable:            "session is no longer usable",
	WorkSessionNotActive:            "selected session is not active",
	WorkSessionExpired:              "selected session has expired",
	WorkSessionScopeNotAllowed:      "session does not include this scope",
	WorkSessionServerNotAllowed:     "target server is not in this session",
	WorkSessionClusterNotAllowed:    "target cluster is not in this session",
	WorkSessionAssigneeMismatch:     "this session is assigned to another principal",
	WorkSessionAgentSessionRequired: "this CLI runs under an AI agent, and an AI agent cannot use a user session",
	WorkSessionAccountNotApproved:   "the command runs as an account the session was not approved for; a session runs only as the account named when it was created",
}

func isWorkSessionCode(code string) bool {
	_, ok := workSessionReasonMap[code]
	return ok
}

// HandleWorkSessionError prints a WorkSession gate diagnostic and exits(3); no-op for other errors.
// target is what the operation names: a cluster for OperationKube, a server otherwise.
func HandleWorkSessionError(err error, operation, target, authMethod, activeWS string) {
	if err == nil {
		return
	}
	code, _ := ParseErrorResponse(err)
	if !isWorkSessionCode(code) {
		return
	}
	hint := RefusalHint(err)
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildWorkSessionErrorEnvelope(code, operation, target, authMethod, activeWS, hint))
	} else {
		fmt.Fprintln(os.Stderr, buildWorkSessionDiagnostic(code, operation, target, authMethod, activeWS, hint))
	}
	os.Exit(ExitCodeWorkSessionDenied)
}

func buildWorkSessionDiagnostic(code, operation, target, authMethod, activeWS, hint string) string {
	reason := workSessionReasonMap[code]
	authDisplay := authMethod
	if authMethod == "Browser login" {
		authDisplay = "Browser login (interactive)"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %s\n", Red("Error"), workSessionMessage(operation))
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "auth", authDisplay)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "reason", reason)
	if operation != "" {
		fmt.Fprintf(&sb, "  %-14s: %s\n", "required scope", operation)
	}
	if target != "" {
		label := "target server"
		if operation == OperationKube {
			label = "target cluster"
		}
		fmt.Fprintf(&sb, "  %-14s: %s\n", label, target)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Next:")
	for _, action := range workSessionGuidance(code, operation, target, activeWS, hint) {
		fmt.Fprintf(&sb, "  %s\n", action.PlainText())
	}
	fmt.Fprintln(&sb)
	if code == WorkSessionAgentSessionRequired {
		fmt.Fprintln(&sb, agentSessionNote(aiagent.FromEnvironment()))
	}
	fmt.Fprint(&sb, workSessionTokenNote(operation))
	return sb.String()
}

func workSessionMessage(operation string) string {
	if operation == OperationKube {
		return "opening a kube session requires a WorkSession in the active state that targets the cluster with the kube scope."
	}
	return fmt.Sprintf("the %s operation requires an active WorkSession on this authentication.", operation)
}

// agentSessionNote tells a person taken for an agent which variables made the
// CLI think so, and how to get out of it.
func agentSessionNote(d aiagent.Detection) string {
	if !d.Detected() {
		return "Note: Not an AI agent? Run the command from your own terminal."
	}
	return fmt.Sprintf("Note: Detected AI agent %s. Not an AI agent? Unset those variables, or run the command from your own terminal.", d.Describe())
}

func workSessionTokenNote(operation string) string {
	switch operation {
	case OperationWebsh:
		return "Note: Tokens issued by Alpacon (service or personal API token) cannot open an interactive terminal; it needs " + LoginWithoutTokenHint + "."
	case OperationKube:
		return "Note: Tokens issued by Alpacon (service or personal API token) cannot open a kube session; it needs " + LoginWithoutTokenHint + "."
	}
	return "Note: Tokens issued by Alpacon (service or personal API token) bypass this check."
}

func buildWorkSessionErrorEnvelope(code, operation, target, authMethod, activeWS, hint string) workSessionErrorJSON {
	var ws *string
	if activeWS != "" {
		ws = &activeWS
	}
	return workSessionErrorJSON{
		OK:        false,
		ExitCode:  ExitCodeWorkSessionDenied,
		ErrorCode: code,
		Message:   workSessionMessage(operation),
		Reason:    workSessionReasonMap[code],
		Context: workSessionErrorCtx{
			AuthMethod: authMethod,
			// Kept even when empty so the JSON shape stays stable.
			RequiredScope:      operation,
			TargetServers:      targetServerList(operation, target),
			CurrentWorksession: ws,
			TargetClusters:     targetClusterList(operation, target),
		},
		NextActions: workSessionGuidance(code, operation, target, activeWS, hint),
	}
}

// workSessionGuidance is the Next list for a WorkSession refusal: the server's
// hint first, when the refusal carried one, then the actions built from the
// code. A code-built description that says what the hint says is dropped, and
// its command stays.
func workSessionGuidance(code, operation, target, activeWS, hint string) []NextAction {
	actions := workSessionNextActions(code, operation, target, activeWS)
	if hint == "" {
		return actions
	}
	for i := range actions {
		if actions[i].Description == accountSessionCreateDescription {
			actions[i].Description = ""
		}
	}
	return append([]NextAction{{Description: hint}}, actions...)
}

func targetServerList(operation, target string) []string {
	if target == "" || operation == OperationKube {
		// Return an empty array (not nil) so the JSON field type stays a stable array.
		return []string{}
	}
	return []string{target}
}

func targetClusterList(operation, clusterName string) []string {
	if operation != OperationKube || clusterName == "" {
		return nil
	}
	return []string{clusterName}
}

func workSessionNextActions(code, operation, target, activeWS string) []NextAction {
	if operation == OperationKube {
		return kubeWorkSessionNextActions(code, target, activeWS)
	}
	serverArg := target
	if serverArg == "" {
		// Some call sites have no target server (e.g. exec logs); keep the suggestion runnable.
		serverArg = "<SERVER>"
	}
	createCmd := fmt.Sprintf(
		`alpacon work-session create --scope %s --server %s --expires-in 1h --purpose "<intent>" --use`,
		operation, serverArg,
	)
	agentScope := AgentSessionScope(operation)
	agentCreateCmd := fmt.Sprintf(
		`alpacon work-session create --scope %s --server %s --expires-in 1h --purpose "<intent>" --requester-type agent`,
		agentScope, serverArg,
	)
	// An agent runs gated work with --work-session <ID>, and websh work through exec instead.
	agentReuse := "prefixing the gated command with --work-session <ID>"
	agentNext := "prefix the gated command with --work-session <ID>"
	agentCreate := NextAction{Command: agentCreateCmd}
	if agentScope != operation {
		agentReuse = fmt.Sprintf("running the work with 'alpacon exec --work-session <ID>' (an agent session cannot hold %s)", operation)
		agentNext = fmt.Sprintf("run the work with 'alpacon exec --work-session <ID>'; an agent session cannot hold %s", operation)
		agentCreate.Description = "AI agent: " + agentNext
	}
	// Reuse an active session first, then create one. Humans attach with `use`; agents cannot, so
	// they pass --work-session <ID> to the gated command.
	createOrReuse := []NextAction{
		{Command: "alpacon work-session ls --status active", Description: "find an existing active session; AI agent: reuse it by " + agentReuse},
		{Command: "alpacon work-session use <ID>", Description: "human: attach an existing session (rejects agent sessions)"},
		{Command: createCmd, Description: "none active? create a new one (human)"},
		{Command: agentCreateCmd, Description: "none active? create a new one (AI agent; " + agentNext + ")"},
	}
	switch code {
	case WorkSessionAccountNotApproved:
		accountCreate := strings.Replace(createCmd, " --use", " --account <account>", 1)
		if aiagent.FromEnvironment().Detected() {
			accountCreate = agentCreateCmd + " --account <account>"
		}
		return []NextAction{
			{Command: "alpacon exec" + workSessionFlag(activeWS) + " -u <approved account> " + serverArg + " -- <command>", Description: "pass -u with the session's account (shown by 'alpacon work-session current')"},
			{Command: accountCreate, Description: accountSessionCreateDescription},
		}
	case WorkSessionAgentSessionRequired:
		// The selected session is a person's; only an agent session serves an agent.
		return []NextAction{
			{Command: "alpacon work-session ls --status active --requester-type agent", Description: "find an active agent session; reuse it by " + agentReuse},
			{Command: agentCreateCmd, Description: "none active? create an agent session (" + agentNext + ")"},
		}
	case WorkSessionRequired:
		return createOrReuse
	case WorkSessionNotActive:
		// Activation is approval/server-driven, not user-run; guide toward an active session.
		return append([]NextAction{
			{Command: "alpacon work-session current", Description: "pending/approved: wait until active; completed/revoked/cancelled: create or reuse below"},
		}, createOrReuse...)
	case WorkSessionExpired:
		return []NextAction{{Command: workSessionExtendCommand(activeWS)}, {Command: createCmd}, agentCreate}
	case WorkSessionAssigneeMismatch:
		// The session belongs to another principal; agents cannot `use`, so take the agent-aware
		// path.
		return createOrReuse
	case WorkSessionNotUsable:
		return createOrReuse
	default: // scope_not_allowed, server_not_allowed
		return []NextAction{{Command: createCmd}, agentCreate}
	}
}

// workSessionFlag carries the selected session into a suggested command, so a
// retry names the session the refused command used; an agent session cannot be
// attached with 'work-session use' and has no other way to select one.
func workSessionFlag(activeWS string) string {
	if activeWS == "" {
		return ""
	}
	return " --work-session " + activeWS
}

func workSessionExtendCommand(activeWS string) string {
	if activeWS != "" {
		return fmt.Sprintf("alpacon work-session extend %s", activeWS)
	}
	return "alpacon work-session extend <ID>"
}

// kubeWorkSessionNextActions suggests a session that targets the cluster with
// the kube scope. 'alpacon kube login' finds or creates one for a person; an
// agent session cannot be attached, so an agent creates its own and names it
// with --work-session.
func kubeWorkSessionNextActions(code, clusterName, activeWS string) []NextAction {
	cluster := clusterName
	if cluster == "" {
		cluster = "<CLUSTER>"
	}
	login := NextAction{
		Command:     "alpacon kube login " + cluster,
		Description: "find or create a work session for this cluster",
	}
	agentCreate := NextAction{
		Command:     fmt.Sprintf(`alpacon work-session create --scope kube --cluster %s --expires-in 1h --purpose "<intent>" --requester-type agent`, cluster),
		Description: fmt.Sprintf("AI agent: create an agent session, then run 'alpacon kube login %s --work-session <ID>'", cluster),
	}
	reuse := NextAction{
		Command:     "alpacon work-session ls --status active",
		Description: fmt.Sprintf("find an active session for this cluster; pass it with 'alpacon kube login %s --work-session <ID>'", cluster),
	}
	switch code {
	case WorkSessionAgentSessionRequired:
		// The selected session is a person's; only an agent session serves an agent.
		return []NextAction{
			{Command: "alpacon work-session ls --status active --requester-type agent", Description: reuse.Description},
			agentCreate,
		}
	case WorkSessionNotActive:
		// A cluster session is never the active one, so 'work-session current'
		// cannot show it; 'kube login' waits on one still pending approval.
		wait := NextAction{Command: fmt.Sprintf("alpacon kube login %s --work-session <ID>", cluster), Description: "pending/approved: waits until it is active; completed/revoked/cancelled: create or reuse below"}
		if activeWS != "" {
			wait.Command = fmt.Sprintf("alpacon kube login %s --work-session %s", cluster, activeWS)
		}
		return []NextAction{wait, reuse, login, agentCreate}
	case WorkSessionExpired:
		return []NextAction{{Command: workSessionExtendCommand(activeWS)}, login, agentCreate}
	default: // required, not_usable, assignee_mismatch, and the scope, cluster, server and account codes
		return []NextAction{reuse, login, agentCreate}
	}
}
