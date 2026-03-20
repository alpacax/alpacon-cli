package cmd

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatHostURL(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{
			name:     "plain hostname gets https",
			host:     "alpacon.example.com",
			expected: "https://alpacon.example.com",
		},
		{
			name:     "direct API URL gets https",
			host:     "myworkspace.us1.alpacon.io",
			expected: "https://myworkspace.us1.alpacon.io",
		},
		{
			name:     "localhost gets http",
			host:     "localhost:8000",
			expected: "http://localhost:8000",
		},
		{
			name:     "127.0.0.1 gets http",
			host:     "127.0.0.1:8000",
			expected: "http://127.0.0.1:8000",
		},
		{
			name:     "already has https prefix",
			host:     "https://myworkspace.us1.alpacon.io",
			expected: "https://myworkspace.us1.alpacon.io",
		},
		{
			name:     "already has http prefix",
			host:     "http://localhost:8000",
			expected: "http://localhost:8000",
		},
		{
			name:     "trailing slash is stripped",
			host:     "alpacon.example.com/",
			expected: "https://alpacon.example.com",
		},
		{
			name:     "https with trailing slash is stripped",
			host:     "https://alpacon.example.com/",
			expected: "https://alpacon.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatHostURL(tt.host)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildCloudWorkspaceURL(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		region    string
		expected  string
	}{
		{
			name:      "us1 region",
			workspace: "myworkspace",
			region:    "us1",
			expected:  "https://myworkspace.us1.alpacon.io",
		},
		{
			name:      "ap1 region",
			workspace: "myworkspace",
			region:    "ap1",
			expected:  "https://myworkspace.ap1.alpacon.io",
		},
		{
			name:      "eu1 region",
			workspace: "testws",
			region:    "eu1",
			expected:  "https://testws.eu1.alpacon.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fmt.Sprintf("https://%s.%s.%s", tt.workspace, tt.region, defaultBaseDomain)
			assert.Equal(t, tt.expected, result)
		})
	}
}
