package utils

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
)

// colorEnabled reports whether stderr is a terminal. Color helpers are
// only used for log/prompt output (which goes to stderr), so gating on
// stderr avoids ANSI artifacts when logs are piped or redirected.
var colorEnabled = term.IsTerminal(int(os.Stderr.Fd()))

// Green returns a bold green string for terminal output.
func Green(value string) string {
	if !colorEnabled {
		return value
	}
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiGreen, value, ansiReset)
}

// Yellow returns a bold yellow string for terminal output.
func Yellow(value string) string {
	if !colorEnabled {
		return value
	}
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiYellow, value, ansiReset)
}

// Blue returns a bold blue string for terminal output.
func Blue(value string) string {
	if !colorEnabled {
		return value
	}
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiBlue, value, ansiReset)
}

// Red returns a bold red string for terminal output.
func Red(value string) string {
	if !colorEnabled {
		return value
	}
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiRed, value, ansiReset)
}

// Bold returns a bold string for terminal output.
func Bold(value string) string {
	if !colorEnabled {
		return value
	}
	return fmt.Sprintf("%s%s%s", ansiBold, value, ansiReset)
}
