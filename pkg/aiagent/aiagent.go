// Package aiagent tells whether an AI coding agent is driving the CLI, so the
// CLI can declare it on every request and open an agent session for it.
//
// Detection reads the environment only: no subprocess and no process walk. An
// agent sets its markers in the shells it spawns, and they survive a tmux or
// daemon in between where a parent-process chain does not. Signals a person
// meets every day never count: no TTY, CI, an editor's integrated terminal
// (TERM_PROGRAM), CLAUDECODE on its own (set in people's editor terminals by
// Claude Code's IDE extensions), REPL_ID, GOOSE_PROVIDER, and AGENT with any
// value but goose. There is no setting that turns detection off.
package aiagent

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const (
	// NameHeader carries the canonical name of the detected agent.
	NameHeader = "X-Alpacon-Declared-Agent-Name"
	// VersionHeader carries the agent's version, only when the environment
	// states one.
	VersionHeader = "X-Alpacon-Declared-Agent-Version"

	// The server keeps at most this many characters of each header.
	maxNameLength    = 64
	maxVersionLength = 32

	// EnvAIAgent is the cross-agent variable: any agent, including one with no
	// marker of its own, can set AI_AGENT=<name> to be detected.
	EnvAIAgent = "AI_AGENT"
)

// Detection is what the environment says about the agent driving the CLI.
type Detection struct {
	// Name is the canonical agent name, empty when no agent was detected.
	Name string
	// Version is the agent's version, empty when the environment carries none.
	Version string
	// Markers lists every variable that marked an agent, in table order. It
	// holds every match, not only the one that named the agent, because a
	// person taken for an agent has to unset all of them.
	Markers []string
}

// Detected reports whether an agent was found.
func (d Detection) Detected() bool {
	return d.Name != ""
}

// Describe names the agent and every marker that found it, for messages:
// "claude-code (from AI_AGENT, CLAUDE_CODE_CHILD_SESSION)".
func (d Detection) Describe() string {
	return fmt.Sprintf("%s (from %s)", d.Name, strings.Join(d.Markers, ", "))
}

// SetHeaders adds the declaration headers to h. It adds nothing when no agent
// was detected. When the version is unknown it sends no version header and
// removes one a caller supplied, so the version is only ever the one the
// environment stated.
func (d Detection) SetHeaders(h http.Header) {
	if !d.Detected() {
		return
	}
	h.Set(NameHeader, d.Name)
	if d.Version != "" {
		h.Set(VersionHeader, d.Version)
		return
	}
	h.Del(VersionHeader)
}

// LookupFunc reads one environment variable, like os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// signal is one environment variable and the values that mark an agent.
type signal struct {
	env     string
	matches func(value string) bool
}

type row struct {
	name    string
	signals []signal
}

func equals(want string) func(string) bool {
	return func(value string) bool { return value == want }
}

func equalsFold(want string) func(string) bool {
	return func(value string) bool { return strings.EqualFold(value, want) }
}

func nonEmpty(value string) bool { return value != "" }

// rows is the detection table after AI_AGENT, in order; the first match names
// the agent.
var rows = []row{
	{name: "claude-code", signals: []signal{{"CLAUDE_CODE_CHILD_SESSION", equals("1")}}},
	{name: "codex", signals: []signal{
		{"CODEX_CI", equals("1")},
		{"CODEX_THREAD_ID", nonEmpty},
		{"CODEX_SANDBOX", nonEmpty},
	}},
	{name: "gemini-cli", signals: []signal{{"GEMINI_CLI", equals("1")}}},
	{name: "github-copilot", signals: []signal{{"COPILOT_AGENT", equals("1")}}},
	{name: "github-copilot", signals: []signal{{"COPILOT_CLI", equals("1")}}},
	{name: "cursor", signals: []signal{{"CURSOR_AGENT", equals("1")}}},
	{name: "cline", signals: []signal{{"CLINE_ACTIVE", equalsFold("true")}}},
	{name: "goose", signals: []signal{
		{"GOOSE_TERMINAL", equals("1")},
		{"AGENT", equalsFold("goose")},
	}},
}

// aliases reduces each name an agent is known to send to one canonical name.
// A name outside the table is lowercased and stands for itself.
var aliases = map[string]string{
	"claude-code":                 "claude-code",
	"codex":                       "codex",
	"codex-mcp-client":            "codex",
	"gemini-cli":                  "gemini-cli",
	"gemini-cli-mcp-client":       "gemini-cli",
	"github-copilot":              "github-copilot",
	"github_copilot_vscode_agent": "github-copilot",
	"github-copilot-cli":          "github-copilot",
	"cursor":                      "cursor",
	"cline":                       "cline",
	"goose":                       "goose",
	"aider":                       "aider",
}

// versionedAgent matches the "<name>_<version>_agent" form Claude Code puts in
// AI_AGENT, such as claude-code_2-1-120_agent. The version starts with a digit,
// so github_copilot_vscode_agent is not read as one.
var versionedAgent = regexp.MustCompile(`^(.+?)_([0-9][^_]*)_(?i:agent)$`)

// dashedVersion is a version written with dashes for dots (2-1-120).
var dashedVersion = regexp.MustCompile(`^[0-9]+(-[0-9]+)+$`)

// FromEnvironment detects the agent from the process environment. It is cheap
// enough to call per request, which keeps it free of process-wide state.
func FromEnvironment() Detection {
	return Detect(os.LookupEnv)
}

// Variables lists every environment variable detection reads, in table order.
func Variables() []string {
	names := []string{EnvAIAgent}
	for _, r := range rows {
		for _, s := range r.signals {
			names = append(names, s.env)
		}
	}
	return names
}

// Detect reads the environment through lookup and returns the agent it names.
func Detect(lookup LookupFunc) Detection {
	get := func(key string) string {
		value, _ := lookup(key)
		return strings.TrimSpace(value)
	}

	var d Detection
	if name, version := parseAIAgent(get(EnvAIAgent)); name != "" {
		d.Name, d.Version = name, version
		d.Markers = append(d.Markers, EnvAIAgent)
	}
	for _, r := range rows {
		for _, s := range r.signals {
			if !s.matches(get(s.env)) {
				continue
			}
			d.Markers = append(d.Markers, s.env)
			if d.Name == "" {
				d.Name = r.name
			}
		}
	}
	return d
}

// parseAIAgent turns an AI_AGENT value into a canonical name and, where the
// value carries one, a version. Only the name is canonicalized; the version
// keeps the case it was written in. An empty name means the value names
// nothing.
func parseAIAgent(value string) (name, version string) {
	value = clean(value, len(value))
	if value == "" {
		return "", ""
	}
	if canonical, ok := aliases[strings.ToLower(value)]; ok {
		return canonical, ""
	}
	if m := versionedAgent.FindStringSubmatch(value); m != nil {
		name, version = m[1], m[2]
	} else if before, after, found := strings.Cut(value, "/"); found {
		name, version = before, after
	} else {
		name = value
	}
	if dashedVersion.MatchString(version) {
		version = strings.ReplaceAll(version, "-", ".")
	}
	name = strings.ToLower(clean(name, maxNameLength))
	if canonical, ok := aliases[name]; ok {
		name = canonical
	}
	if name == "" {
		return "", ""
	}
	return name, clean(version, maxVersionLength)
}

// clean keeps printable ASCII only, so the value is always a valid header
// value, then trims it and cuts it to limit characters.
func clean(value string, limit int) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > limit {
		out = strings.TrimSpace(out[:limit])
	}
	return out
}
