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
	Command     string              `json:"command"`
	ErrorCode   string              `json:"error_code"`
	Message     string              `json:"message"`
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
		fmt.Fprintln(os.Stderr, buildWorkSessionDiagnostic(code, operation, serverName, authMethod))
	}
	os.Exit(ExitCodeWorkSessionDenied)
}

func buildWorkSessionDiagnostic(code, operation, serverName, authMethod string) string {
	reason := workSessionReasonMap[code]
	authDisplay := authMethod
	if authMethod == "Browser login" {
		authDisplay = "Browser login (interactive)"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %s requires an active WorkSession on this authentication.\n", Red("Error"), operation)
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "auth", authDisplay)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "reason", reason)
	fmt.Fprintf(&sb, "  %-14s: %s\n", "required scope", operation)
	if serverName != "" {
		fmt.Fprintf(&sb, "  %-14s: %s\n", "target server", serverName)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Next:")
	for _, action := range workSessionNextActions(code, operation, serverName) {
		fmt.Fprintf(&sb, "  %s\n", action)
	}
	fmt.Fprintln(&sb)
	fmt.Fprint(&sb, "Note: ServiceToken or personal API token (source != login) skip this requirement.")
	return sb.String()
}

func buildWorkSessionJSON(code, operation, serverName, authMethod, activeWS string) string {
	var ws *string
	if activeWS != "" {
		ws = &activeWS
	}
	envelope := workSessionErrorJSON{
		OK:        false,
		Command:   operation,
		ErrorCode: code,
		Message:   fmt.Sprintf("%s requires an active WorkSession on this authentication.", operation),
		Context: workSessionErrorCtx{
			AuthMethod:         authMethod,
			RequiredScope:      operation,
			TargetServers:      targetServerList(serverName),
			CurrentWorksession: ws,
		},
		NextActions: workSessionNextActions(code, operation, serverName),
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

func workSessionNextActions(code, operation, serverName string) []string {
	createCmd := fmt.Sprintf(
		`alpacon worksession create --scope %s --server %s --purpose "<intent>"`,
		operation, serverName,
	)
	switch code {
	case WorkSessionRequired:
		return []string{
			"alpacon worksession list --status active",
			"alpacon worksession use <ID>",
			createCmd,
		}
	case WorkSessionNotActive:
		return []string{"alpacon worksession current"}
	case WorkSessionExpired:
		return []string{"alpacon worksession extend <ID>", createCmd}
	case WorkSessionAssigneeMismatch:
		return []string{"alpacon worksession use <ID>"}
	case WorkSessionNotUsable:
		return []string{
			"alpacon worksession list",
			createCmd,
		}
	default: // scope_not_allowed, server_not_allowed
		return []string{createCmd}
	}
}
