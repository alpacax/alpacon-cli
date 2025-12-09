package utils

import (
	"encoding/json"
	"strings"
)

const (
	CodeAuthMFARequired = "auth_mfa_required"
	UsernameRequired    = "user_username_required"
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

	start := strings.Index(errStr, "{")
	if start == -1 {
		return "", ""
	}

	jsonPart := errStr[start:]

	var errorResp ErrorResponse
	if jsonErr := json.Unmarshal([]byte(jsonPart), &errorResp); jsonErr != nil {
		return "", ""
	}

	return errorResp.Code, errorResp.Source
}
