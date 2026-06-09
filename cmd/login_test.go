package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type promptCall struct {
	prompt       string
	defaultValue string
	required     bool
}

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
			name:     "already has uppercase HTTPS prefix",
			host:     "HTTPS://myworkspace.us1.alpacon.io",
			expected: "HTTPS://myworkspace.us1.alpacon.io",
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
		{
			name:     "bare localhost gets http",
			host:     "localhost",
			expected: "http://localhost",
		},
		{
			name:     "localhost-prefixed host stays https",
			host:     "localhost.example.com",
			expected: "https://localhost.example.com",
		},
		{
			name:     "127.0.0.1-prefixed host stays https",
			host:     "127.0.0.1.attacker.com",
			expected: "https://127.0.0.1.attacker.com",
		},
		{
			name:     "uppercase localhost gets http",
			host:     "LOCALHOST:8000",
			expected: "http://LOCALHOST:8000",
		},
		{
			name:     "mixed-case localhost gets http",
			host:     "Localhost",
			expected: "http://Localhost",
		},
		{
			name:     "surrounding whitespace is trimmed",
			host:     "  alpacon.example.com  ",
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

func TestValidateHostTarget(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "plain host", host: "alpacon.example.com", wantErr: false},
		{name: "host with scheme and port", host: "https://alpacon.example.com:8443", wantErr: false},
		{name: "empty host", host: "", wantErr: true},
		{name: "whitespace only", host: "   ", wantErr: true},
		{name: "port only", host: ":8443", wantErr: true},
		{name: "path is rejected", host: "alpacon.example.com/login", wantErr: true},
		{name: "query is rejected", host: "alpacon.example.com?a=b", wantErr: true},
		{name: "userinfo is rejected", host: "admin@alpacon.example.com", wantErr: true},
		{name: "http scheme allowed", host: "http://alpacon.example.com", wantErr: false},
		{name: "non-http(s) scheme is rejected", host: "ssh://alpacon.example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHostTarget(tt.host)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
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

func TestParseCloudWorkspaceURL(t *testing.T) {
	tests := []struct {
		name          string
		workspaceURL  string
		wantWorkspace string
		wantRegion    string
		wantOK        bool
	}{
		{
			name:          "cloud URL with us1",
			workspaceURL:  "https://demo.us1.alpacon.io",
			wantWorkspace: "demo",
			wantRegion:    "us1",
			wantOK:        true,
		},
		{
			name:          "cloud URL with ap1 and trailing slash",
			workspaceURL:  "https://demo.ap1.alpacon.io/",
			wantWorkspace: "demo",
			wantRegion:    "ap1",
			wantOK:        true,
		},
		{
			name:          "cloud URL with mixed-case base domain",
			workspaceURL:  "https://demo.us1.Alpacon.io",
			wantWorkspace: "demo",
			wantRegion:    "us1",
			wantOK:        true,
		},
		{
			name:         "self-hosted URL is not cloud",
			workspaceURL: "https://alpacon.example.com",
		},
		{
			name:          "future region is still parsed from cloud URL shape",
			workspaceURL:  "https://demo.eu1.alpacon.io",
			wantWorkspace: "demo",
			wantRegion:    "eu1",
			wantOK:        true,
		},
		{
			name:         "missing workspace is not cloud",
			workspaceURL: "https://us1.alpacon.io",
		},
		{
			name:         "empty URL is not cloud",
			workspaceURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace, region, ok := parseCloudWorkspaceURL(tt.workspaceURL)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantWorkspace, workspace)
			assert.Equal(t, tt.wantRegion, region)
		})
	}
}

func TestCloudLoginDefaults(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		wantWorkspace string
		wantRegion    string
	}{
		{
			name:          "uses workspace and region from cloud URL",
			cfg:           config.Config{WorkspaceURL: "https://demo.ap1.alpacon.io"},
			wantWorkspace: "demo",
			wantRegion:    "ap1",
		},
		{
			name:          "URL workspace wins over stale workspace name",
			cfg:           config.Config{WorkspaceURL: "https://demo.us1.alpacon.io", WorkspaceName: "stale"},
			wantWorkspace: "demo",
			wantRegion:    "us1",
		},
		{
			name:          "falls back to saved workspace name",
			cfg:           config.Config{WorkspaceName: "saved"},
			wantWorkspace: "saved",
			wantRegion:    knownCloudRegions[0],
		},
		{
			name:       "empty config uses default region",
			cfg:        config.Config{},
			wantRegion: knownCloudRegions[0],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace, region := cloudLoginDefaults(tt.cfg)
			assert.Equal(t, tt.wantWorkspace, workspace)
			assert.Equal(t, tt.wantRegion, region)
		})
	}
}

func TestIsCloudWorkspaceURL(t *testing.T) {
	tests := []struct {
		name         string
		workspaceURL string
		expected     bool
	}{
		{
			name:         "canonical cloud URL",
			workspaceURL: "https://demo.us1.alpacon.io",
			expected:     true,
		},
		{
			name:         "cloud host without scheme normalizes to canonical URL",
			workspaceURL: "demo.us1.alpacon.io",
			expected:     true,
		},
		{
			name:         "mixed-case cloud base domain is canonical",
			workspaceURL: "https://demo.us1.Alpacon.io",
			expected:     true,
		},
		{
			name:         "mixed-case cloud scheme is canonical",
			workspaceURL: "HTTPS://demo.us1.alpacon.io",
			expected:     true,
		},
		{
			name:         "http cloud-shaped URL is non-canonical",
			workspaceURL: "http://demo.us1.alpacon.io",
			expected:     false,
		},
		{
			name:         "cloud-shaped URL with port is non-canonical",
			workspaceURL: "https://demo.us1.alpacon.io:8443",
			expected:     false,
		},
		{
			name:         "cloud-shaped URL with path is non-canonical",
			workspaceURL: "https://demo.us1.alpacon.io/foo",
			expected:     false,
		},
		{
			name:         "self-hosted URL",
			workspaceURL: "https://alpacon.example.com",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCloudWorkspaceURL(tt.workspaceURL)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPromptForLoginTarget(t *testing.T) {
	t.Run("saved cloud target prompts with workspace and region defaults", func(t *testing.T) {
		workspaceURL, workspaceName, baseDomain, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://demo.ap1.alpacon.io",
		}, "", "")

		require.NoError(t, err)
		assert.Equal(t, "https://demo.ap1.alpacon.io", workspaceURL)
		assert.Equal(t, "demo", workspaceName)
		assert.Equal(t, defaultBaseDomain, baseDomain)
		assert.Equal(t, []promptCall{
			{prompt: "Workspace name [demo]: ", defaultValue: "demo"},
			{prompt: "Region [ap1] (us1, ap1): ", defaultValue: "ap1"},
		}, calls)
	})

	t.Run("saved self-hosted target prompts with host default and preserves base domain", func(t *testing.T) {
		workspaceURL, workspaceName, baseDomain, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL:  "https://tenant.private.example.com",
			BaseDomain:    "private.example.com",
			WorkspaceName: "tenant",
		}, "")

		require.NoError(t, err)
		assert.Equal(t, "https://tenant.private.example.com", workspaceURL)
		assert.Equal(t, "tenant", workspaceName)
		assert.Equal(t, "private.example.com", baseDomain)
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.private.example.com]: ", defaultValue: "https://tenant.private.example.com"},
		}, calls)
	})

	t.Run("saved self-hosted target preserves workspace name when host is unchanged", func(t *testing.T) {
		workspaceURL, workspaceName, baseDomain, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL:  "https://gateway.example.com",
			WorkspaceName: "tenant",
		}, "")

		require.NoError(t, err)
		assert.Equal(t, "https://gateway.example.com", workspaceURL)
		assert.Equal(t, "tenant", workspaceName)
		assert.Equal(t, "example.com", baseDomain)
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://gateway.example.com]: ", defaultValue: "https://gateway.example.com"},
		}, calls)
	})

	t.Run("edited custom-domain target under saved base domain preserves base domain", func(t *testing.T) {
		workspaceURL, workspaceName, baseDomain, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL:  "https://tenant.us1.private.example.com",
			BaseDomain:    "private.example.com",
			WorkspaceName: "tenant",
		}, "https://other.us1.private.example.com")

		require.NoError(t, err)
		assert.Equal(t, "https://other.us1.private.example.com", workspaceURL)
		assert.Equal(t, "other", workspaceName)
		assert.Equal(t, "private.example.com", baseDomain)
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.us1.private.example.com]: ", defaultValue: "https://tenant.us1.private.example.com"},
		}, calls)
	})

	t.Run("non-canonical cloud-shaped target is confirmed as host to avoid URL rewrite", func(t *testing.T) {
		workspaceURL, workspaceName, baseDomain, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "http://demo.us1.alpacon.io:8443",
		}, "")

		require.NoError(t, err)
		assert.Equal(t, "http://demo.us1.alpacon.io:8443", workspaceURL)
		assert.Equal(t, "demo", workspaceName)
		assert.Equal(t, defaultBaseDomain, baseDomain)
		assert.Equal(t, []promptCall{
			{prompt: "Host [http://demo.us1.alpacon.io:8443]: ", defaultValue: "http://demo.us1.alpacon.io:8443"},
		}, calls)
	})

	t.Run("saved host default with path returns path validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://demo.us1.alpacon.io/foo",
		}, "")

		require.Error(t, err)
		assert.ErrorContains(t, err, "paths, queries, and fragments are not supported")
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://demo.us1.alpacon.io/foo]: ", defaultValue: "https://demo.us1.alpacon.io/foo"},
		}, calls)
	})

	t.Run("typed host with path returns path validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://tenant.private.example.com",
		}, "alpacon.example.com/foo")

		require.Error(t, err)
		assert.ErrorContains(t, err, "paths, queries, and fragments are not supported")
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.private.example.com]: ", defaultValue: "https://tenant.private.example.com"},
		}, calls)
	})

	t.Run("typed host with query returns host validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://tenant.private.example.com",
		}, "alpacon.example.com?x=1")

		require.Error(t, err)
		assert.ErrorContains(t, err, "paths, queries, and fragments are not supported")
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.private.example.com]: ", defaultValue: "https://tenant.private.example.com"},
		}, calls)
	})

	t.Run("typed host with fragment returns host validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://tenant.private.example.com",
		}, "alpacon.example.com#frag")

		require.Error(t, err)
		assert.ErrorContains(t, err, "paths, queries, and fragments are not supported")
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.private.example.com]: ", defaultValue: "https://tenant.private.example.com"},
		}, calls)
	})

	t.Run("typed host with invalid escaped path returns host validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://tenant.private.example.com",
		}, "alpacon.example.com/%zz")

		require.Error(t, err)
		assert.ErrorContains(t, err, "paths, queries, and fragments are not supported")
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.private.example.com]: ", defaultValue: "https://tenant.private.example.com"},
		}, calls)
	})

	t.Run("typed host with invalid escaped query returns host validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://tenant.private.example.com",
		}, "alpacon.example.com?x=%zz")

		require.Error(t, err)
		assert.ErrorContains(t, err, "paths, queries, and fragments are not supported")
		assert.Equal(t, []promptCall{
			{prompt: "Host [https://tenant.private.example.com]: ", defaultValue: "https://tenant.private.example.com"},
		}, calls)
	})

	t.Run("typed workspace with path returns validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{}, "demo/foo", "")

		require.Error(t, err)
		assert.ErrorContains(t, err, "workspace must contain only letters, numbers, and hyphens")
		assert.Equal(t, []promptCall{
			{prompt: "Workspace name: ", required: true},
			{prompt: "Region [us1] (us1, ap1): ", defaultValue: "us1"},
		}, calls)
	})

	t.Run("typed region with path returns validation error", func(t *testing.T) {
		_, _, _, calls, err := runPromptForLoginTarget(t, config.Config{
			WorkspaceURL: "https://demo.us1.alpacon.io",
		}, "", "us1/foo")

		require.Error(t, err)
		assert.ErrorContains(t, err, "region must contain only letters, numbers, and hyphens")
		assert.Equal(t, []promptCall{
			{prompt: "Workspace name [demo]: ", defaultValue: "demo"},
			{prompt: "Region [us1] (us1, ap1): ", defaultValue: "us1"},
		}, calls)
	})

	t.Run("empty config requires workspace and defaults region", func(t *testing.T) {
		workspaceURL, workspaceName, baseDomain, calls, err := runPromptForLoginTarget(t, config.Config{}, "demo", "")

		require.NoError(t, err)
		assert.Equal(t, "https://demo.us1.alpacon.io", workspaceURL)
		assert.Equal(t, "demo", workspaceName)
		assert.Equal(t, defaultBaseDomain, baseDomain)
		assert.Equal(t, []promptCall{
			{prompt: "Workspace name: ", required: true},
			{prompt: "Region [us1] (us1, ap1): ", defaultValue: "us1"},
		}, calls)
	})
}

func TestValidateInteractiveLoginTargetPrompt(t *testing.T) {
	tests := []struct {
		name          string
		isInteractive bool
		wantErr       bool
	}{
		{name: "bare no-target login can prompt in an interactive shell", isInteractive: true},
		{name: "bare no-target login errors in non-interactive mode", wantErr: true},
		{name: "token without target still errors in non-interactive mode", wantErr: true},
		{name: "no-browser without target still errors in non-interactive mode", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInteractiveLoginTargetPrompt(tt.isInteractive)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, "login target is required in non-interactive mode")
				assert.ErrorContains(t, err, "--workspace and --region")
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestLoginNonInteractiveNoTargetCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare no-target login with saved config", args: []string{"login"}},
		{name: "token without target with saved config", args: []string{"login", "-t", "token-value"}},
		{name: "no-browser without target with saved config", args: []string{"login", "--no-browser"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, exitCode := runLoginCommandHelper(t, tt.args)

			assert.Equal(t, 1, exitCode)
			assert.Contains(t, stderr, "login target is required in non-interactive mode")
			assert.Contains(t, stderr, "--workspace and --region")
			assert.NotContains(t, stderr, "Using saved workspace")
			assert.NotContains(t, stderr, "Workspace '")
			assert.NotContains(t, stderr, "Device code request failed")
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
		{name: "host with path", args: []string{"alpacon.io/demo"}, wantErrSub: "paths, queries, and fragments are not supported"},
		{name: "host with query", args: []string{"alpacon.example.com?x=1"}, wantErrSub: "paths, queries, and fragments are not supported"},
		{name: "host with fragment", args: []string{"alpacon.example.com#frag"}, wantErrSub: "paths, queries, and fragments are not supported"},
		{name: "host with userinfo", args: []string{"admin@alpacon.example.com"}, wantErrSub: "credentials"},
		{name: "host with non-http(s) scheme", args: []string{"ssh://alpacon.example.com"}, wantErrSub: "scheme must be http or https"},
		{name: "host with invalid escaped path", args: []string{"alpacon.example.com/%zz"}, wantErrSub: "paths, queries, and fragments are not supported"},
		{name: "host with invalid escaped query", args: []string{"alpacon.example.com?x=%zz"}, wantErrSub: "paths, queries, and fragments are not supported"},
		{name: "cloud direct url accepted", args: []string{"demo.us1.alpacon.io"}, wantOK: true, wantURL: "https://demo.us1.alpacon.io", wantName: "demo", wantDomain: "alpacon.io"},
		{name: "cloud direct url with path reports path error", args: []string{"demo.us1.alpacon.io/foo"}, wantErrSub: "paths, queries, and fragments are not supported"},
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
		{
			name:      "workspace with path",
			workspace: "demo/foo",
			region:    "us1",
			contains:  []string{"workspace must contain only letters, numbers, and hyphens"},
		},
		{
			name:      "region with path",
			workspace: "demo",
			region:    "us1/foo",
			contains:  []string{"region must contain only letters, numbers, and hyphens"},
		},
		{
			name:      "workspace URL is rejected",
			workspace: "https://demo.us1.alpacon.io",
			region:    "us1",
			contains:  []string{"workspace must contain only letters, numbers, and hyphens"},
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

func runPromptForLoginTarget(t *testing.T, cfg config.Config, responses ...string) (workspaceURL, workspaceName, baseDomain string, calls []promptCall, err error) {
	t.Helper()

	nextResponse := func() string {
		require.NotEmpty(t, responses, "missing fake prompt response")
		response := responses[0]
		responses = responses[1:]
		return response
	}

	promptWithDefault := func(promptText, defaultValue string) string {
		calls = append(calls, promptCall{prompt: promptText, defaultValue: defaultValue})
		response := nextResponse()
		if response == "" {
			return defaultValue
		}
		return response
	}
	promptRequired := func(promptText string) string {
		calls = append(calls, promptCall{prompt: promptText, required: true})
		response := nextResponse()
		require.NotEmpty(t, response, "fake required prompt response cannot be empty")
		return response
	}

	workspaceURL, workspaceName, baseDomain, err = promptForLoginTargetWithPrompts(cfg, promptWithDefault, promptRequired)
	require.Empty(t, responses, "unused fake prompt responses")
	return workspaceURL, workspaceName, baseDomain, calls, err
}

func runLoginCommandHelper(t *testing.T, args []string) (stdout, stderr string, exitCode int) {
	t.Helper()

	home := t.TempDir()
	writeLoginCommandTestConfig(t, home)

	helperArgs := append(
		[]string{"-test.run=^TestLoginCommandHelperProcess$", "--", "login-helper"},
		args...,
	)
	helper := osexec.Command(os.Args[0], helperArgs...)
	helper.Env = append(os.Environ(),
		"GO_WANT_LOGIN_HELPER=1",
		"HOME="+home,
	)

	var stdoutBuf, stderrBuf bytes.Buffer
	helper.Stdout = &stdoutBuf
	helper.Stderr = &stderrBuf

	err := helper.Run()
	exitCode = 0
	if err != nil {
		var exitErr *osexec.ExitError
		require.True(t, errors.As(err, &exitErr), "expected exit error, got %T: %v", err, err)
		exitCode = exitErr.ExitCode()
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func TestLoginCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOGIN_HELPER") != "1" {
		return
	}
	args, ok := loginCommandHelperArgs(os.Args)
	if !ok {
		os.Exit(2)
	}
	RootCmd.SetArgs(args)
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func loginCommandHelperArgs(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "login-helper" {
			return args[i+1:], true
		}
	}
	return nil, false
}

func writeLoginCommandTestConfig(t *testing.T, home string) {
	t.Helper()

	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))

	cfg := map[string]any{
		"workspace_url":           "https://saved.us1.alpacon.io",
		"workspace_name":          "saved",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"base_domain":             defaultBaseDomain,
		"insecure":                false,
		"active_work_sessions":    map[string]string{},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600))
}
