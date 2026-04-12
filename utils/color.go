package utils

import "fmt"

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
)

// Green returns a bold green string for terminal output.
func Green(value string) string {
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiGreen, value, ansiReset)
}

// Yellow returns a bold yellow string for terminal output.
func Yellow(value string) string {
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiYellow, value, ansiReset)
}

// Blue returns a bold blue string for terminal output.
func Blue(value string) string {
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiBlue, value, ansiReset)
}

// Red returns a bold red string for terminal output.
func Red(value string) string {
	return fmt.Sprintf("%s%s%s%s", ansiBold, ansiRed, value, ansiReset)
}

// Bold returns a bold string for terminal output.
func Bold(value string) string {
	return fmt.Sprintf("%s%s%s", ansiBold, value, ansiReset)
}
