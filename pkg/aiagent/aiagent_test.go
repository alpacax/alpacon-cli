package aiagent

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func lookupFrom(env map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}

func TestDetect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		env         map[string]string
		wantName    string
		wantVersion string
		wantMarkers []string
	}{
		{
			name:        "AI_AGENT names any agent",
			env:         map[string]string{"AI_AGENT": "aider"},
			wantName:    "aider",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT is lowercased",
			env:         map[string]string{"AI_AGENT": "  Aider "},
			wantName:    "aider",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT carries the Claude Code version",
			env:         map[string]string{"AI_AGENT": "claude-code_2-1-120_agent"},
			wantName:    "claude-code",
			wantVersion: "2.1.120",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT dotted version",
			env:         map[string]string{"AI_AGENT": "claude-code_2.1.120_agent"},
			wantName:    "claude-code",
			wantVersion: "2.1.120",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT name and version separated by a slash",
			env:         map[string]string{"AI_AGENT": "codex/0.50.0"},
			wantName:    "codex",
			wantVersion: "0.50.0",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT version keeps its case",
			env:         map[string]string{"AI_AGENT": "my-agent/1.0.0-Beta.1"},
			wantName:    "my-agent",
			wantVersion: "1.0.0-Beta.1",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT name is lowercased, version is not",
			env:         map[string]string{"AI_AGENT": "My-Agent/2.0.0-RC1"},
			wantName:    "my-agent",
			wantVersion: "2.0.0-RC1",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT Claude Code form in upper case",
			env:         map[string]string{"AI_AGENT": "Claude-Code_2-1-120_AGENT"},
			wantName:    "claude-code",
			wantVersion: "2.1.120",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT upper-case alias",
			env:         map[string]string{"AI_AGENT": "GITHUB_COPILOT_VSCODE_AGENT"},
			wantName:    "github-copilot",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT upper-case alias with a version",
			env:         map[string]string{"AI_AGENT": "Codex-MCP-Client/0.50.0"},
			wantName:    "codex",
			wantVersion: "0.50.0",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT VS Code Copilot alias",
			env:         map[string]string{"AI_AGENT": "github_copilot_vscode_agent"},
			wantName:    "github-copilot",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT MCP client alias",
			env:         map[string]string{"AI_AGENT": "gemini-cli-mcp-client"},
			wantName:    "gemini-cli",
			wantMarkers: []string{"AI_AGENT"},
		},
		{
			name:        "AI_AGENT outranks every other row",
			env:         map[string]string{"AI_AGENT": "aider", "CODEX_CI": "1"},
			wantName:    "aider",
			wantMarkers: []string{"AI_AGENT", "CODEX_CI"},
		},
		{
			name:        "Claude Code child session",
			env:         map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1"},
			wantName:    "claude-code",
			wantMarkers: []string{"CLAUDE_CODE_CHILD_SESSION"},
		},
		{
			name:        "Claude Code names every marker found",
			env:         map[string]string{"AI_AGENT": "claude-code_2-1-120_agent", "CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDECODE": "1"},
			wantName:    "claude-code",
			wantVersion: "2.1.120",
			wantMarkers: []string{"AI_AGENT", "CLAUDE_CODE_CHILD_SESSION"},
		},
		{
			name:        "Codex CI",
			env:         map[string]string{"CODEX_CI": "1"},
			wantName:    "codex",
			wantMarkers: []string{"CODEX_CI"},
		},
		{
			name:        "Codex thread",
			env:         map[string]string{"CODEX_THREAD_ID": "thread-1"},
			wantName:    "codex",
			wantMarkers: []string{"CODEX_THREAD_ID"},
		},
		{
			name:        "Codex sandbox",
			env:         map[string]string{"CODEX_SANDBOX": "seatbelt"},
			wantName:    "codex",
			wantMarkers: []string{"CODEX_SANDBOX"},
		},
		{
			name:        "Gemini CLI",
			env:         map[string]string{"GEMINI_CLI": "1"},
			wantName:    "gemini-cli",
			wantMarkers: []string{"GEMINI_CLI"},
		},
		{
			name:        "Copilot agent",
			env:         map[string]string{"COPILOT_AGENT": "1"},
			wantName:    "github-copilot",
			wantMarkers: []string{"COPILOT_AGENT"},
		},
		{
			name:        "Copilot CLI",
			env:         map[string]string{"COPILOT_CLI": "1"},
			wantName:    "github-copilot",
			wantMarkers: []string{"COPILOT_CLI"},
		},
		{
			name:        "Cursor",
			env:         map[string]string{"CURSOR_AGENT": "1"},
			wantName:    "cursor",
			wantMarkers: []string{"CURSOR_AGENT"},
		},
		{
			name:        "Cline",
			env:         map[string]string{"CLINE_ACTIVE": "true"},
			wantName:    "cline",
			wantMarkers: []string{"CLINE_ACTIVE"},
		},
		{
			name:        "Goose terminal",
			env:         map[string]string{"GOOSE_TERMINAL": "1"},
			wantName:    "goose",
			wantMarkers: []string{"GOOSE_TERMINAL"},
		},
		{
			name:        "AGENT set to goose",
			env:         map[string]string{"AGENT": "goose"},
			wantName:    "goose",
			wantMarkers: []string{"AGENT"},
		},
		{
			name:        "first row wins and later markers are still named",
			env:         map[string]string{"GEMINI_CLI": "1", "CURSOR_AGENT": "1"},
			wantName:    "gemini-cli",
			wantMarkers: []string{"GEMINI_CLI", "CURSOR_AGENT"},
		},
		{
			name:        "markers past an unreadable AI_AGENT still name the agent",
			env:         map[string]string{"AI_AGENT": "\x01\x02", "CODEX_CI": "1"},
			wantName:    "codex",
			wantMarkers: []string{"CODEX_CI"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Detect(lookupFrom(tt.env))
			assert.Equal(t, tt.wantName, got.Name)
			assert.Equal(t, tt.wantVersion, got.Version)
			assert.Equal(t, tt.wantMarkers, got.Markers)
			assert.True(t, got.Detected())
		})
	}
}

// A person meets these signals every day, so none of them may put the CLI on
// the agent path.
func TestDetect_NeverAnAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"empty environment, as with no TTY", map[string]string{}},
		{"CLAUDECODE alone", map[string]string{"CLAUDECODE": "1"}},
		{"VS Code terminal", map[string]string{"TERM_PROGRAM": "vscode"}},
		{"other editor terminal", map[string]string{"TERM_PROGRAM": "cursor"}},
		{"CI", map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}},
		{"AGENT with another value", map[string]string{"AGENT": "foo"}},
		{"Replit shell", map[string]string{"REPL_ID": "repl-1"}},
		{"Goose configured but not driving", map[string]string{"GOOSE_PROVIDER": "openai"}},
		{"blank AI_AGENT", map[string]string{"AI_AGENT": "   "}},
		{"markers set to other values", map[string]string{
			"CLAUDE_CODE_CHILD_SESSION": "0",
			"CODEX_CI":                  "0",
			"GEMINI_CLI":                "true",
			"COPILOT_AGENT":             "",
			"COPILOT_CLI":               "0",
			"CURSOR_AGENT":              "0",
			"CLINE_ACTIVE":              "false",
			"GOOSE_TERMINAL":            "0",
			"CODEX_THREAD_ID":           "",
			"CODEX_SANDBOX":             " ",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Detect(lookupFrom(tt.env))
			assert.False(t, got.Detected(), "detected %q from %v", got.Name, got.Markers)
			assert.Empty(t, got.Markers)
		})
	}
}

func TestVariablesCoversEveryRow(t *testing.T) {
	t.Parallel()
	env := map[string]string{}
	for _, name := range Variables() {
		env[name] = "1"
	}
	env["AGENT"] = "goose"
	env["CLINE_ACTIVE"] = "true"
	assert.Len(t, Detect(lookupFrom(env)).Markers, len(Variables()))
}

func TestSetHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		detection   Detection
		wantName    string
		wantVersion string
	}{
		{"name and version", Detection{Name: "claude-code", Version: "2.1.120", Markers: []string{"AI_AGENT"}}, "claude-code", "2.1.120"},
		{"name only", Detection{Name: "codex", Markers: []string{"CODEX_CI"}}, "codex", ""},
		{"nothing detected", Detection{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			tt.detection.SetHeaders(h)
			assert.Equal(t, tt.wantName, h.Get(NameHeader))
			assert.Equal(t, tt.wantVersion, h.Get(VersionHeader))
			if tt.wantVersion == "" {
				assert.NotContains(t, h, VersionHeader)
			}
			if tt.wantName == "" {
				assert.Empty(t, h)
			}
		})
	}
}

func TestParseAIAgent_FitsTheHeaders(t *testing.T) {
	t.Parallel()
	name, version := parseAIAgent(strings.Repeat("a", 100) + "/" + strings.Repeat("1", 50))
	assert.Len(t, name, maxNameLength)
	assert.Len(t, version, maxVersionLength)

	name, version = parseAIAgent("my\r\nagent\x00/1.0\t")
	assert.Equal(t, "myagent", name)
	assert.Equal(t, "1.0", version)
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	d := Detection{Name: "claude-code", Markers: []string{"AI_AGENT", "CLAUDE_CODE_CHILD_SESSION"}}
	assert.Equal(t, "claude-code (from AI_AGENT, CLAUDE_CODE_CHILD_SESSION)", d.Describe())
}
