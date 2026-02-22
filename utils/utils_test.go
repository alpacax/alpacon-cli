package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBoolPointerToString(t *testing.T) {
	trueVal := true
	falseVal := false

	assert.Equal(t, "null", BoolPointerToString(nil))
	assert.Equal(t, "true", BoolPointerToString(&trueVal))
	assert.Equal(t, "false", BoolPointerToString(&falseVal))
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		str      string
		num      int
		expected string
	}{
		{"longer than limit", "hello world", 5, "hello..."},
		{"exactly at limit", "hello", 5, "hello"},
		{"shorter than limit", "hi", 10, "hi"},
		{"empty string", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TruncateString(tt.str, tt.num))
		})
	}
}

func TestIsUUID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid UUID v4", "550e8400-e29b-41d4-a716-446655440000", true},
		{"valid UUID uppercase", "550E8400-E29B-41D4-A716-446655440000", true},
		{"plain name", "my-server", false},
		{"empty string", "", false},
		{"partial UUID", "550e8400-e29b-41d4", false},
		{"UUID without dashes", "550e8400e29b41d4a716446655440000", true}, // uuid.Parse accepts 32-char hex
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsUUID(tt.input))
		})
	}
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name         string
		basePath     string
		relativePath string
		params       map[string]string
		wantSuffix   string
	}{
		{"base only", "/api/servers/servers/", "", nil, "/api/servers/servers/"},
		{"base with id", "/api/servers/servers/", "abc-123", nil, "/api/servers/servers/abc-123/"},
		{"base with params", "/api/servers/servers/", "", map[string]string{"name": "my-server"}, "/api/servers/servers/?name=my-server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildURL(tt.basePath, tt.relativePath, tt.params)
			assert.Contains(t, result, tt.wantSuffix)
		})
	}
}

func TestTimeUtils(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{"zero time", time.Time{}, "None"},
		{"30 seconds ago", now.Add(-30 * time.Second), "just now"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"yesterday", now.Add(-30 * time.Hour), "yesterday"},
		{"3 days ago", now.Add(-72 * time.Hour), "3 days ago"},
		{"in a few seconds", now.Add(30 * time.Second), "in a few seconds"},
		{"in 5 minutes", now.Add(5*time.Minute + 30*time.Second), "in 5 minutes"},
		{"in 3 hours", now.Add(3*time.Hour + 30*time.Minute), "in 3 hours"},
		{"tomorrow", now.Add(30 * time.Hour), "tomorrow"},
		{"in 3 days", now.Add(72*time.Hour + 30*time.Minute), "in 3 days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TimeUtils(tt.input))
		})
	}
}

func TestExtractWorkspaceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard URL", "https://myws.us1.alpacon.io", "myws"},
		{"no subdomain", "https://alpacon.io", "alpacon"},
		{"empty string", "", ""},
		{"localhost", "http://localhost:8000", "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ExtractWorkspaceName(tt.input))
		})
	}
}

func TestRemovePrefixBeforeAPI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"full URL", "https://example.com/api/servers/", "/api/servers/"},
		{"already relative", "/api/test/", "/api/test/"},
		{"no /api/", "no-api-here", "no-api-here"},
		{"api in middle", "prefix/api/resource/", "/api/resource/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, RemovePrefixBeforeAPI(tt.input))
		})
	}
}
