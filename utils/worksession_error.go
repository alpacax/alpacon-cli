package utils

import (
	"fmt"
	"os"
	"strings"
)

// Operations HandleWorkSessionError reports; each but OperationKube is also the WorkSession scope it requires.
const (
	OperationCommand = "command"
	OperationWebFTP  = "webftp"
	OperationWebsh   = "websh"
	// OperationKube names no scope: any active WorkSession of the caller qualifies.
	OperationKube = "kube"
)

type workSessionErrorJSON = JSONErrorEnvelope[workSessionErrorCtx]

type workSessionErrorCtx struct {
	AuthMethod         string   `json:"auth_method"`
	RequiredScope      string   `json:"required_scope"`
	TargetServers      []string `json:"target_servers"`
	CurrentWorksession *string  `json:"current_worksession"`
}

var workSessionReasonMap = map[string]string{
	WorkSessionRequired:         "no WorkSession selected for this shell",
	WorkSessionNotUsable:        "session is no longer usable",
	WorkSessionNotActive:        "selected session is not active",
	WorkSessionExpired:          "selected session has expired",
	WorkSessionScopeNotAllowed:  "session does not include this scope",
	WorkSessionServerNotAllowed: "target server is not in this session",
	WorkSessionAssigneeMismatch: "this session is assigned to another principal",
}

func isWorkSessionCode(code string) bool {
	_, ok := workSessionReasonMap[code]
	return ok
}

// HandleWorkSessionError prints a WorkSession gate diagnostic and exits(3); no-op for other errors.
func HandleWorkSessionError(err error, operation, serverName, authMethod, activeWS string) {
	if err == nil {
		return
	}
	code, _ := ParseErrorResponse(err)
	if !isWorkSessionCode(code) {
		return
	}
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildWorkSessionErrorEnvelope(code, operation, serverName, authMethod, activeWS))
	} else {
		fmt.Fprintln(os.Stderr, buildWorkSessionDiagnostic(code, operation, serverName, authMethod, activeWS))
	}
	os.Exit(ExitCodeWorkSessionDenied)
}

func buildWorkSessionDiagnostic(code, operation, serverName, authMethod, activeWS string) string {
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
	if scope := workSessionRequiredScope(operation); scope != "" {
		fmt.Fprintf(&sb, "  %-14s: %s\n", "required scope", scope)
	}
	if serverName != "" {
		fmt.Fprintf(&sb, "  %-14s: %s\n", "target server", serverName)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Next:")
	for _, action := range workSessionNextActions(code, operation, serverName, activeWS) {
		fmt.Fprintf(&sb, "  %s\n", action.PlainText())
	}
	fmt.Fprintln(&sb)
	fmt.Fprint(&sb, workSessionTokenNote(operation))
	return sb.String()
}

func workSessionMessage(operation string) string {
	if operation == OperationKube {
		return "opening a kube session requires an active WorkSession on this authentication."
	}
	return fmt.Sprintf("the %s operation requires an active WorkSession on this authentication.", operation)
}

// workSessionRequiredScope is empty for kube, which any active WorkSession satisfies.
func workSessionRequiredScope(operation string) string {
	if operation == OperationKube {
		return ""
	}
	return operation
}

func workSessionTokenNote(operation string) string {
	switch operation {
	case OperationWebsh:
		return "Note: Tokens issued by Alpacon (service or personal API token) cannot open an interactive terminal; it needs 'alpacon login' without -t."
	case OperationKube:
		return "Note: Tokens issued by Alpacon (service or personal API token) cannot open a kube session; it needs 'alpacon login' without -t."
	}
	return "Note: Tokens issued by Alpacon (service or personal API token) bypass this check."
}

func buildWorkSessionErrorEnvelope(code, operation, serverName, authMethod, activeWS string) workSessionErrorJSON {
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
			RequiredScope:      workSessionRequiredScope(operation),
			TargetServers:      targetServerList(serverName),
			CurrentWorksession: ws,
		},
		NextActions: workSessionNextActions(code, operation, serverName, activeWS),
	}
}

func targetServerList(serverName string) []string {
	if serverName == "" {
		// Return an empty array (not nil) so the JSON field type stays a stable array.
		return []string{}
	}
	return []string{serverName}
}

func workSessionNextActions(code, operation, serverName, activeWS string) []NextAction {
	if operation == OperationKube {
		return kubeWorkSessionNextActions(code, activeWS)
	}
	serverArg := serverName
	if serverArg == "" {
		// Some call sites have no target server (e.g. exec logs); keep the suggestion runnable.
		serverArg = "<SERVER>"
	}
	createCmd := fmt.Sprintf(
		`alpacon work-session create --scope %s --server %s --expires-in 1h --purpose "<intent>" --use`,
		operation, serverArg,
	)
	agentCreateCmd := fmt.Sprintf(
		`alpacon work-session create --scope %s --server %s --expires-in 1h --purpose "<intent>" --requester-type agent`,
		operation, serverArg,
	)
	// Reuse an active session first (ls), then create as the fallback. Humans attach via `use`;
	// agents can't `use` (it rejects agent sessions) and instead pass --work-session <ID> downstream.
	createOrReuse := []NextAction{
		{Command: "alpacon work-session ls --status active", Description: "find an existing active session; AI agent: reuse it by prefixing the gated command with --work-session <ID>"},
		{Command: "alpacon work-session use <ID>", Description: "human: attach an existing session (rejects agent sessions)"},
		{Command: createCmd, Description: "none active? create a new one (human)"},
		{Command: agentCreateCmd, Description: "none active? create a new one (AI agent; prefix the gated command with --work-session <ID>)"},
	}
	switch code {
	case WorkSessionRequired:
		return createOrReuse
	case WorkSessionNotActive:
		// Activation is approval/server-driven, not user-run; guide toward an active session.
		return append([]NextAction{
			{Command: "alpacon work-session current", Description: "pending/approved: wait until active; completed/revoked/cancelled: create or reuse below"},
		}, createOrReuse...)
	case WorkSessionExpired:
		return []NextAction{{Command: workSessionExtendCommand(activeWS)}, {Command: createCmd}, {Command: agentCreateCmd}}
	case WorkSessionAssigneeMismatch:
		// Session belongs to another principal; reuse/create one owned by this caller.
		// Agents can't `use`, so route through the agent-aware reuse+create path.
		return createOrReuse
	case WorkSessionNotUsable:
		return createOrReuse
	default: // scope_not_allowed, server_not_allowed
		return []NextAction{{Command: createCmd}, {Command: agentCreateCmd}}
	}
}

func workSessionExtendCommand(activeWS string) string {
	if activeWS != "" {
		return fmt.Sprintf("alpacon work-session extend %s", activeWS)
	}
	return "alpacon work-session extend <ID>"
}

// kubeWorkSessionNextActions suggests a session the caller owns, whatever its scope.
// An AI-agent WorkSession cannot open a kube session, so no agent path is offered.
func kubeWorkSessionNextActions(code, activeWS string) []NextAction {
	create := NextAction{
		Command:     `alpacon work-session create --scope command --server <SERVER> --expires-in 1h --purpose "<intent>" --use`,
		Description: "none active? create one; any active session you own works for kube",
	}
	reuseOrCreate := []NextAction{
		{Command: "alpacon work-session ls --status active", Description: "find an existing active session you own"},
		{Command: "alpacon work-session use <ID>", Description: "attach it"},
		create,
	}
	switch code {
	case WorkSessionNotActive:
		return append([]NextAction{
			{Command: "alpacon work-session current", Description: "pending/approved: wait until active; completed/revoked/cancelled: create or reuse below"},
		}, reuseOrCreate...)
	case WorkSessionExpired:
		return []NextAction{{Command: workSessionExtendCommand(activeWS)}, create}
	default: // required, not_usable, assignee_mismatch, and the scope/server codes kube never names
		return reuseOrCreate
	}
}
