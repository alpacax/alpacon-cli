package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type workSessionErrorJSON struct {
	OK          bool                `json:"ok"`
	ExitCode    int                 `json:"exit_code"`
	ErrorCode   string              `json:"error_code"`
	Message     string              `json:"message"`
	Reason      string              `json:"reason"`
	Context     workSessionErrorCtx `json:"context"`
	NextActions []string            `json:"next_actions"`
}

type workSessionErrorCtx struct {
	AuthMethod         string   `json:"auth_method"`
	RequiredScope      string   `json:"required_scope"`
	TargetServers      []string `json:"target_servers"`
	CurrentWorksession *string  `json:"current_worksession"`
}

var workSessionReasonMap = map[string]string{
	WorkSessionRequired:         "no WorkSession selected for this shell",
	WorkSessionNotUsable:        "session is no longer usable",
	WorkSessionNotActive:        "selected session is not yet active",
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
		fmt.Fprintln(os.Stderr, buildWorkSessionJSON(code, operation, serverName, authMethod, activeWS))
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

func buildWorkSessionJSON(code, operation, serverName, authMethod, activeWS string) string {
	var ws *string
	if activeWS != "" {
		ws = &activeWS
	}
	envelope := workSessionErrorJSON{
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
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope); err != nil {
		return `{"ok":false,"error_code":"` + code + `"}`
	}
	return strings.TrimRight(buf.String(), "\n")
}

func targetServerList(serverName string) []string {
	if serverName == "" {
		return nil
	}
	return []string{serverName}
}

func workSessionNextActions(code, operation, serverName, activeWS string) []string {
	createCmd := fmt.Sprintf(
		`alpacon work-session create --scope %s --server %s --expires-in 1h --purpose "<intent>"`,
		operation, serverName,
	)
	switch code {
	case WorkSessionRequired:
		return []string{
			"alpacon work-session ls --status active",
			"alpacon work-session use <ID>",
			createCmd,
		}
	case WorkSessionNotActive:
		return []string{"alpacon work-session current"}
	case WorkSessionExpired:
		extendCmd := "alpacon work-session extend <ID>"
		if activeWS != "" {
			extendCmd = fmt.Sprintf("alpacon work-session extend %s", activeWS)
		}
		return []string{extendCmd, createCmd}
	case WorkSessionAssigneeMismatch:
		return []string{"alpacon work-session use <ID>"}
	case WorkSessionNotUsable:
		return []string{
			"alpacon work-session ls",
			createCmd,
		}
	default: // scope_not_allowed, server_not_allowed
		return []string{createCmd}
	}
}
