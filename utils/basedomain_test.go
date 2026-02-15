package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractBaseDomain(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard workspace URL with region",
			input:    "https://myws.ap1.alpacon.io",
			expected: "alpacon.io",
		},
		{
			name:     "Dev environment URL",
			input:    "https://myws.dev.alpacon.io",
			expected: "alpacon.io",
		},
		{
			name:     "US region URL",
			input:    "https://myws.us1.alpacon.io",
			expected: "alpacon.io",
		},
		{
			name:     "Two-part hostname (no subdomain)",
			input:    "https://alpacon.io",
			expected: "",
		},
		{
			name:     "Single-part hostname (localhost)",
			input:    "http://localhost:8000",
			expected: "",
		},
		{
			name:     "Self-hosted with custom domain",
			input:    "https://alpacon.company.com",
			expected: "company.com",
		},
		{
			name:     "Deep subdomain",
			input:    "https://ws.region.sub.example.com",
			expected: "example.com",
		},
		{
			name:     "Invalid URL",
			input:    "://not-a-url",
			expected: "",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "URL with trailing slash",
			input:    "https://myws.ap1.alpacon.io/",
			expected: "alpacon.io",
		},
		{
			name:     "URL with path",
			input:    "https://myws.ap1.alpacon.io/some/path",
			expected: "alpacon.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBaseDomain(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
