package utils

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// DecodeJWTPayload decodes the payload (second segment) of a JWT token
// without verifying the signature. Returns the claims as a map.
func DecodeJWTPayload(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	payload := parts[1]

	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %v", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse JWT payload: %v", err)
	}

	return claims, nil
}
