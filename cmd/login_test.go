package cmd

import (
	"net/http"
	"net/http/httptest"
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

func TestValidateWorkspaceReachability(t *testing.T) {
	t.Run("reachable server returns nil", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		err := validateWorkspaceReachability(ts.URL, ts.Client())
		assert.NoError(t, err)
	})

	t.Run("server returning 404 returns error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		err := validateWorkspaceReachability(ts.URL, ts.Client())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "returned HTTP 404")
	})

	t.Run("unreachable server returns error", func(t *testing.T) {
		err := validateWorkspaceReachability("http://127.0.0.1:1", &http.Client{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unreachable")
	})
}
