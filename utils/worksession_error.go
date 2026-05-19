package utils

import (
	"fmt"
	"strings"
)

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

// HandleWorkSessionError — placeholder until Task 6
func HandleWorkSessionError(err error, operation, serverName, authMethod, activeWS string) {}

func buildWorkSessionDiagnostic(code, operation, serverName, authMethod, activeWS string) string {
	reason := workSessionReasonMap[code]
	authDisplay := authMethod
	if authMethod == "Browser login" {
		authDisplay = "Browser login (interactive)"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Error: %s requires an active WorkSession on this authentication.\n", operation)
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
	return ""
}

func workSessionNextActions(code, operation, serverName string) []string {
	createCmd := fmt.Sprintf(
		`alpacon worksession create --scope %s --server %s --description "<intent>"`,
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
	default: // scope_not_allowed, server_not_allowed, not_usable
		return []string{createCmd}
	}
}
