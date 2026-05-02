package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWelcomeLines(t *testing.T) {
	t.Run("not logged in (no config file)", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		lines := buildWelcomeLines()
		require.Len(t, lines, 3)
		assert.Contains(t, lines[0], "alpacon")
		assert.Contains(t, lines[1], "Not logged in")
		assert.Contains(t, lines[2], "--help")
	})

	t.Run("logged in via Auth0 — workspace host shown", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://myws.us1.alpacon.io","workspace_name":"myws","access_token":"a"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		require.Len(t, lines, 3)
		assert.Equal(t, "myws.us1.alpacon.io", lines[1])
	})

	t.Run("logged in via legacy token — workspace host shown", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://myws.alpacon.io","workspace_name":"myws","token":"t"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		assert.Equal(t, "myws.alpacon.io", lines[1])
	})

	t.Run("config malformed — surfaces config error, not 'not logged in'", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not-json"), 0600))

		lines := buildWelcomeLines()
		assert.Contains(t, lines[1], "Config read error")
		assert.NotContains(t, lines[1], "Not logged in")
	})

	t.Run("config exists but no tokens — treated as not logged in", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfgDir := filepath.Join(home, ".alpacon")
		require.NoError(t, os.MkdirAll(cfgDir, 0700))
		cfg := `{"workspace_url":"https://x.alpacon.io","workspace_name":"x"}`
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600))

		lines := buildWelcomeLines()
		assert.Contains(t, lines[1], "Not logged in")
	})
}

func TestHostFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"https with subdomain", "https://myws.us1.alpacon.io", "myws.us1.alpacon.io"},
		{"http", "http://example.com", "example.com"},
		{"with path and query", "https://myws.alpacon.io/some/path?x=1", "myws.alpacon.io"},
		{"with port", "https://localhost:8080", "localhost:8080"},
		{"no scheme falls back to raw input", "myws.alpacon.io", "myws.alpacon.io"},
		{"empty", "", ""},
		{"malformed url with scheme returns input", "https://", "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hostFromURL(tt.input))
		})
	}
}
