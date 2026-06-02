package cmd

import (
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCloudWorkspaceURL(tt.workspace, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateCloudFlags(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		region    string
		contains  []string
		valid     bool
	}{
		{
			name:      "both set is valid",
			workspace: "demo",
			region:    "us1",
			valid:     true,
		},
		{
			name:      "both empty is valid",
			workspace: "",
			region:    "",
			valid:     true,
		},
		{
			name:      "workspace without region",
			workspace: "demo",
			region:    "",
			contains:  []string{"--region is required", "us1, ap1", "alpacon login --workspace demo --region us1"},
		},
		{
			name:      "region without workspace",
			workspace: "",
			region:    "ap1",
			contains:  []string{"--workspace is required", "--region ap1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCloudFlags(tt.workspace, tt.region)
			if tt.valid {
				assert.NoError(t, err)
				return
			}
			for _, sub := range tt.contains {
				assert.ErrorContains(t, err, sub)
			}
		})
	}
}

func TestIsCloudDirectURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{name: "cloud subdomain", url: "https://demo.us1.alpacon.io", expected: true},
		{name: "cloud subdomain with port", url: "https://demo.us1.alpacon.io:8443", expected: true},
		{name: "cloud region subdomain", url: "https://foo.alpacon.io", expected: true},
		{name: "cloud base domain", url: "https://alpacon.io", expected: true},
		{name: "self-hosted domain", url: "https://alpacon.example.com", expected: false},
		{name: "localhost", url: "http://localhost:8000", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isCloudDirectURL(tt.url))
		})
	}
}
