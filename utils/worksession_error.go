package utils

import (
	"fmt"
	"os"
	"strings"
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
	fmt.Fprintf(&sb, "%s: the %s operation requires an active WorkSession on this authentication.\n", Red("Error"), operation)
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "auth", authDisplay)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "reason", reason)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "required scope", operation)
	if serverName != "" {
		fmt.Fprintf(&sb, "  %-14s: %s\n", "target server", serverName)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Next:")
	for _, action := range workSessionNextActions(code, operation, serverName, activeWS) {
		fmt.Fprintf(&sb, "  %s\n", action)
	}
	fmt.Fprintln(&sb)
	fmt.Fprint(&sb, "Note: Tokens issued by Alpacon (service or personal API token) bypass this check.")
	return sb.String()
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
		Message:   fmt.Sprintf("the %s operation requires an active WorkSession on this authentication.", operation),
		Reason:    workSessionReasonMap[code],
		Context: workSessionErrorCtx{
			AuthMethod:         authMethod,
			RequiredScope:      operation,
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

func workSessionNextActions(code, operation, serverName, activeWS string) []string {
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
	// Reuse first (ls → use/export), then create as the fallback—for human and agent alike.
	createOrReuse := []string{
		"alpacon work-session ls --status active  # find an existing active session",
		"alpacon work-session use <ID>  # human: attach an existing session (rejects agent sessions)",
		"export ALPACON_WORK_SESSION=<ID>  # agent: use an existing session",
		createCmd + "  # none active? create a new one (human)",
		agentCreateCmd + "  # none active? create a new one (AI agent; not attachable via --use)",
	}
	switch code {
	case WorkSessionRequired:
		return createOrReuse
	case WorkSessionNotActive:
		// Activation is approval/server-driven, not user-run; guide toward an active session.
		return append([]string{
			"alpacon work-session current  # pending: wait until active; completed/revoked: create or reuse below",
		}, createOrReuse...)
	case WorkSessionExpired:
		extendCmd := "alpacon work-session extend <ID>"
		if activeWS != "" {
			extendCmd = fmt.Sprintf("alpacon work-session extend %s", activeWS)
		}
		return []string{extendCmd, createCmd}
	case WorkSessionAssigneeMismatch:
		return []string{"alpacon work-session use <ID>"}
	case WorkSessionNotUsable:
		return createOrReuse
	default: // scope_not_allowed, server_not_allowed
		return []string{createCmd}
	}
}
