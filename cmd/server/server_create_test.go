package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/stretchr/testify/assert"
)

func TestPrintTokenChoices_StripsControlSequences(t *testing.T) {
	var buf bytes.Buffer
	printTokenChoices(&buf, []server.RegistrationTokenDetails{
		{Name: "staging\n  [2] production"},
		{Name: "web\x1b[2K\rprod"},
	})

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\r")
	// The prompt, two tokens, and the create-new line: a newline must not forge a choice.
	assert.Equal(t, 4, strings.Count(got, "\n"))
	assert.Contains(t, got, "  [1] staging  [2] production\n")
	assert.Contains(t, got, "  [2] webprod\n")
}

func TestPrintGuideFields_StripsControlSequences(t *testing.T) {
	var buf bytes.Buffer
	printGuideFields(&buf, "Debian\x1b[2K", "web-01\nURL      : https://evil.example.com", "https://demo.alpacon.io")

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.Equal(t, 3, strings.Count(got, "\n"), "one line per field")
	assert.Contains(t, got, "  Platform : Debian\n")
	assert.Contains(t, got, "  Server   : web-01URL      : https://evil.example.com\n")
}
