package utils

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
	return ""
}

func buildWorkSessionJSON(code, operation, serverName, authMethod, activeWS string) string {
	return ""
}

func workSessionNextActions(code, operation, serverName string) []string {
	return nil
}
