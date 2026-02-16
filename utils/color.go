package utils

import (
	"github.com/gookit/color"
)

// Green converts a string to green color in the console with bold text.
func Green(value string) string {
	return color.New(color.FgGreen, color.Bold).Sprint(value)
}

// Yellow converts a string to yellow color in the console with bold text.
func Yellow(value string) string {
	return color.New(color.FgYellow, color.Bold).Sprint(value)
}

// Blue converts a string to blue color in the console with bold text.
func Blue(value string) string {
	return color.New(color.FgBlue, color.Bold).Sprint(value)
}

// Red converts a string to red color in the console with bold text.
func Red(value string) string {
	return color.New(color.FgRed, color.Bold).Sprint(value)
}

// Bold converts a string to bold text in the console.
func Bold(value string) string {
	return color.New(color.Bold).Sprint(value)
}
