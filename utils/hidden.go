package utils

import (
	"fmt"
	"strings"
)

// HiddenCount reads a count of targets the server left unnamed. Nil, sent by
// a server older than Alpacon Server 2.48.0, counts as none.
func HiddenCount(hidden *int) int {
	if hidden == nil || *hidden < 0 {
		return 0
	}
	return *hidden
}

// JoinReached joins the names of the targets the caller reaches and says how
// many more the server counted without naming them, so a partial list is not
// read as the whole scope: "web-1, db-1 and 2 more", or "2 servers you cannot
// access" when no name is left. noun is the singular of what is counted. With
// nothing hidden it is a plain join, unchanged.
//
// Names are not sanitized here; a name that sanitizes to blank is dropped
// only when a count follows, so the count never trails an empty list.
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
