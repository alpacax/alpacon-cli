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

// colorEnabled gates on stderr, where colored log and prompt output goes, so piped or redirected
// logs carry no ANSI codes.
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
