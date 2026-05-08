package worksession

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// parseExpiryFlag validates the --expires-in / --expires-at mutual exclusion
// and returns an RFC3339 expires_at string.
func parseExpiryFlag(expiresIn, expiresAt string) (string, error) {
	if expiresIn != "" && expiresAt != "" {
		return "", errors.New("--expires-in and --expires-at are mutually exclusive")
	}
	if expiresIn == "" && expiresAt == "" {
		return "", errors.New("one of --expires-in or --expires-at is required")
	}
	if expiresIn != "" {
		d, err := time.ParseDuration(expiresIn)
		if err != nil {
			return "", fmt.Errorf("invalid --expires-in value %q: %w", expiresIn, err)
		}
		return time.Now().UTC().Add(d).Format(time.RFC3339), nil
	}
	return expiresAt, nil
}

// validateAgentScopes returns an error when requester_type is "agent" and
// scopes contains "websh", which the server disallows.
func validateAgentScopes(requesterType string, scopes []string) error {
	if requesterType != "agent" {
		return nil
	}
	for _, s := range scopes {
		if s == "websh" {
			return errors.New("scope \"websh\" is not allowed for agent requester type")
		}
	}
	return nil
}

// splitCSV splits a comma-separated string and trims whitespace.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
