package utils

import (
	"fmt"
	"strings"
)

// HiddenCount reads a count of targets the server left unnamed; nil (a server older than
// Alpacon Server 2.48.0) and a negative value count as none.
func HiddenCount(hidden *int) int {
	if hidden == nil || *hidden < 0 {
		return 0
	}
	return *hidden
}

// JoinReached joins names and says how many more are hidden ("web-1 and 2 more", or "2 servers
// you cannot access" with no name left); noun is singular, and names are returned unsanitized.
func JoinReached(names []string, hidden *int, noun string) string {
	n := HiddenCount(hidden)
	if n == 0 {
		return strings.Join(names, ", ")
	}
	shown := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(SanitizeTerminalText(name)) != "" {
			shown = append(shown, name)
		}
	}
	if len(shown) == 0 {
		if n != 1 {
			noun += "s"
		}
		return fmt.Sprintf("%d %s you cannot access", n, noun)
	}
	return fmt.Sprintf("%s and %d more", strings.Join(shown, ", "), n)
}
