package utils

import (
	"fmt"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
)

func (s *IOStreams) paint(codes, value string) string {
	if !s.ColorEnabled {
		return value
	}
	return fmt.Sprintf("%s%s%s", codes, value, ansiReset)
}

// Green returns a bold green string when s has color.
func (s *IOStreams) Green(value string) string { return s.paint(ansiBold+ansiGreen, value) }

// Yellow returns a bold yellow string when s has color.
func (s *IOStreams) Yellow(value string) string { return s.paint(ansiBold+ansiYellow, value) }

// Blue returns a bold blue string when s has color.
func (s *IOStreams) Blue(value string) string { return s.paint(ansiBold+ansiBlue, value) }

// Red returns a bold red string when s has color.
func (s *IOStreams) Red(value string) string { return s.paint(ansiBold+ansiRed, value) }

// Bold returns a bold string when s has color.
func (s *IOStreams) Bold(value string) string { return s.paint(ansiBold, value) }

// Green returns a bold green string for terminal output.
func Green(value string) string { return Stdio.Green(value) }

// Yellow returns a bold yellow string for terminal output.
func Yellow(value string) string { return Stdio.Yellow(value) }

// Blue returns a bold blue string for terminal output.
func Blue(value string) string { return Stdio.Blue(value) }

// Red returns a bold red string for terminal output.
func Red(value string) string { return Stdio.Red(value) }

// Bold returns a bold string for terminal output.
func Bold(value string) string { return Stdio.Bold(value) }
