package utils

import (
	"encoding/json"
	"strings"
)

const (
	AuthMFARequired  = "auth_mfa_required"
	UsernameRequired = "user_username_required"
)

type ErrorResponse struct {
	Code   string `json:"code"`
	Source string `json:"source"`
}

func ParseErrorResponse(err error) (code, source string) {
	if err == nil {
		return "", ""
	}

	errStr := err.Error()

	// Try JSON format: {"code": "...", "source": "..."}
	start := strings.Index(errStr, "{")
	if start != -1 {
		var errorResp ErrorResponse
		if jsonErr := json.Unmarshal([]byte(errStr[start:]), &errorResp); jsonErr == nil && errorResp.Code != "" {
			return errorResp.Code, errorResp.Source
		}
	}

	// Try "code: X; source: Y" format (produced by parseAPIError in the HTTP client)
	for _, part := range strings.Split(errStr, "; ") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "code: ") {
			code = strings.TrimPrefix(part, "code: ")
		} else if strings.HasPrefix(part, "source: ") {
			source = strings.TrimPrefix(part, "source: ")
		}
	}

	return code, source
}
