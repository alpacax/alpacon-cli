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

func TestNormalizeCloudFlags(t *testing.T) {
	tests := []struct {
		name          string
		workspace     string
		region        string
		wantWorkspace string
		wantRegion    string
		wantBlank     bool
	}{
		{name: "trims surrounding spaces", workspace: " demo ", region: " us1 ", wantWorkspace: "demo", wantRegion: "us1"},
		{name: "no flags is not blank", workspace: "", region: "", wantWorkspace: "", wantRegion: ""},
		{name: "whitespace-only both is blank", workspace: " ", region: "  ", wantBlank: true},
		{name: "whitespace-only region only is blank", workspace: "", region: " ", wantBlank: true},
		{name: "valid workspace with blank region is not blank", workspace: "demo", region: " ", wantWorkspace: "demo", wantRegion: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, r, blank := normalizeCloudFlags(tt.workspace, tt.region)
			assert.Equal(t, tt.wantWorkspace, w)
			assert.Equal(t, tt.wantRegion, r)
			assert.Equal(t, tt.wantBlank, blank)
		})
	}
}

func TestResolveLoginTarget(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		workspace  string
		region     string
		wantOK     bool
		wantURL    string
		wantName   string
		wantDomain string
		wantErrSub string
	}{
		{name: "self-hosted host", args: []string{"alpacon.example.com"}, wantOK: true, wantURL: "https://alpacon.example.com", wantName: "alpacon", wantDomain: "example.com"},
		{name: "cloud flags", workspace: "demo", region: "us1", wantOK: true, wantURL: "https://demo.us1.alpacon.io", wantName: "demo", wantDomain: "alpacon.io"},
		{name: "no args no flags falls back", wantOK: false},
		{name: "host plus workspace flag", args: []string{"alpacon.example.com"}, workspace: "demo", wantErrSub: "cannot combine a HOST"},
		{name: "host plus region flag", args: []string{"alpacon.example.com"}, region: "us1", wantErrSub: "cannot combine a HOST"},
		// Blank guard fires before the combine guard, even when a HOST is present.
		{name: "host plus blank flag is blank not combine", args: []string{"alpacon.example.com"}, workspace: " ", wantErrSub: "cannot be blank"},
		{name: "blank both flags", workspace: " ", region: " ", wantErrSub: "cannot be blank"},
		{name: "blank region only", region: " ", wantErrSub: "cannot be blank"},
		{name: "workspace without region", workspace: "demo", wantErrSub: "--region is required"},
		{name: "region without workspace", region: "us1", wantErrSub: "--workspace is required"},
		{name: "host with path", args: []string{"alpacon.io/demo"}, wantErrSub: "URL paths are not supported"},
		{name: "cloud direct url accepted", args: []string{"demo.us1.alpacon.io"}, wantOK: true, wantURL: "https://demo.us1.alpacon.io", wantName: "demo", wantDomain: "alpacon.io"},
		// Path check runs first, so a cloud URL with a path reports the path error.
		{name: "cloud direct url with path reports path error", args: []string{"demo.us1.alpacon.io/foo"}, wantErrSub: "URL paths are not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, name, domain, ok, err := resolveLoginTarget(tt.args, tt.workspace, tt.region)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				assert.False(t, ok)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantURL, url)
			}
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, name)
			}
			if tt.wantDomain != "" {
				assert.Equal(t, tt.wantDomain, domain)
			}
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
