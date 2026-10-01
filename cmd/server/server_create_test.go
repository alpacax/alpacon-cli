package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintTokenChoices_StripsControlSequences(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printTokenChoices(&buf, []server.RegistrationTokenDetails{
		{Name: "staging\n  [2] production"},
		{Name: "web\x1b[2K\rprod"},
	})

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\r")
	// The prompt, two tokens, and the create-new line: a newline must not forge a choice.
	assert.Equal(t, 4, strings.Count(got, "\n"))
	assert.Contains(t, got, "  [1] staging  [2] production\n")
	assert.Contains(t, got, "  [2] webprod\n")
}

func TestPrintGuideFields_StripsControlSequences(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printGuideFields(&buf, "Debian\x1b[2K", "web-01\nURL      : https://evil.example.com", "https://demo.alpacon.io")

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.Equal(t, 3, strings.Count(got, "\n"), "one line per field")
	assert.Contains(t, got, "  Platform : Debian\n")
	assert.Contains(t, got, "  Server   : web-01URL      : https://evil.example.com\n")
}

func captureGuideStderr(t *testing.T, fn func()) string {
	t.Helper()
	_, stderr := testutil.CaptureOutput(t, fn)
	return stderr
}

// The guide strings are what the operator pastes into a root shell, so the
// warning has to name the command it is about—one banner at the top would not.
func TestPrintGuideCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantLine    string
		wantWarning bool
	}{
		{
			name:     "prints a clean command untouched and without a warning",
			input:    "curl -fsSL https://demo.alpacon.io/i.sh | sudo bash",
			wantLine: "curl -fsSL https://demo.alpacon.io/i.sh | sudo bash\n",
		},
		{
			name:     "keeps a multi-line snippet pasteable",
			input:    "[servers]\nweb-01 ansible_host=10.0.0.1",
			wantLine: "[servers]\nweb-01 ansible_host=10.0.0.1\n",
		},
		{
			name:        "warns above a command that carried an escape",
			input:       "curl real.example.com\x1b[2Kcurl evil.example.com",
			wantLine:    "curl real.example.comcurl evil.example.com\n",
			wantWarning: true,
		},
		{
			name:        "warns above a command that carried a bidi override",
			input:       "curl \u202ereal.example.com",
			wantLine:    "curl real.example.com\n",
			wantWarning: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printGuideCommand(&buf, tt.input)

			got := buf.String()
			assert.True(t, strings.HasSuffix(got, tt.wantLine), "the command must be the last thing printed: %q", got)
			if tt.wantWarning {
				assert.Contains(t, got, "Warning")
			} else {
				assert.NotContains(t, got, "Warning")
			}
		})
	}
}

// Every command the guide echoes has to go through the same door; a new one
// added to the response and printed straight would slip past unnoticed.
func TestDisplayAnsibleGuideFromJSON_WarnsOnEveryAlteredField(t *testing.T) {
	fields := []struct {
		name  string
		build func(string) server.AnsibleGuideJsonResponse
	}{
		{name: "CollectionInstall", build: func(v string) server.AnsibleGuideJsonResponse {
			return server.AnsibleGuideJsonResponse{CollectionInstall: v}
		}},
		{name: "InventorySnippet", build: func(v string) server.AnsibleGuideJsonResponse {
			return server.AnsibleGuideJsonResponse{InventorySnippet: v}
		}},
		{name: "RunCommandQuick", build: func(v string) server.AnsibleGuideJsonResponse {
			return server.AnsibleGuideJsonResponse{RunCommandQuick: v}
		}},
		{name: "PlaybookSnippet", build: func(v string) server.AnsibleGuideJsonResponse {
			return server.AnsibleGuideJsonResponse{PlaybookSnippet: v}
		}},
		{name: "RunCommandCustom", build: func(v string) server.AnsibleGuideJsonResponse {
			return server.AnsibleGuideJsonResponse{RunCommandCustom: v}
		}},
	}
	for _, f := range fields {
		t.Run(f.name, func(t *testing.T) {
			got := captureGuideStderr(t, func() {
				displayAnsibleGuideFromJSON(f.build("ansible-galaxy install\x1b[2Kevil"))
			})

			assert.NotContains(t, got, "\x1b[2K")
			assert.Contains(t, got, "Warning")
		})
	}
}

func TestDisplayGuideFromJSON_WarnsOnEveryAlteredField(t *testing.T) {
	fields := []struct {
		name  string
		build func(string) server.RegistrationMethodGuideJsonResponse
	}{
		{name: "InstallCommands", build: func(v string) server.RegistrationMethodGuideJsonResponse {
			return server.RegistrationMethodGuideJsonResponse{InstallCommands: []string{v}}
		}},
		{name: "RegisterCommand", build: func(v string) server.RegistrationMethodGuideJsonResponse {
			return server.RegistrationMethodGuideJsonResponse{RegisterCommand: v}
		}},
	}
	for _, f := range fields {
		t.Run(f.name, func(t *testing.T) {
			got := captureGuideStderr(t, func() {
				displayGuideFromJSON(f.build("curl real.example.com\x1b[2Kevil"))
			})

			assert.NotContains(t, got, "\x1b[2K")
			assert.Contains(t, got, "Warning")
		})
	}
}

func TestDisplayGuideFromJSON_StaysQuietOnACleanGuide(t *testing.T) {
	got := captureGuideStderr(t, func() {
		displayGuideFromJSON(server.RegistrationMethodGuideJsonResponse{
			InstallCommands: []string{"curl -fsSL https://demo.alpacon.io/i.sh | sudo bash"},
			RegisterCommand: "alpamon register --token abc",
		})
	})

	assert.NotContains(t, got, "Warning")
	assert.Contains(t, got, "curl -fsSL https://demo.alpacon.io/i.sh | sudo bash\n")
	assert.Contains(t, got, "alpamon register --token abc\n")
}

func TestResolvePlatform_AcceptsSuseFromFlag(t *testing.T) {
	prev := createPlatform
	t.Cleanup(func() { createPlatform = prev })
	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().StringVarP(&createPlatform, "platform", "p", "", "")
	require.NoError(t, cmd.Flags().Parse([]string{"--platform", "suse"}))

	got := resolvePlatform(cmd, "token-install")

	assert.Equal(t, "suse", got)
}

func TestCheckPlatform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		platform string
		wantErr  string
	}{
		{name: "token-install accepts suse", method: "token-install", platform: "suse"},
		{name: "token-install accepts debian", method: "token-install", platform: "debian"},
		{name: "ansible accepts rhel", method: "ansible", platform: "rhel"},
		{name: "ansible rejects suse", method: "ansible", platform: "suse", wantErr: `"suse" is not supported with the ansible method (valid values: debian, rhel, darwin, windows)`},
		{name: "token-install rejects an unknown platform", method: "token-install", platform: "arch", wantErr: `"arch" is not a platform (valid values: debian, rhel, suse, darwin, windows)`},
		{name: "ansible rejects an empty platform", method: "ansible", platform: "", wantErr: `"" is not a platform (valid values: debian, rhel, darwin, windows)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkPlatform(tt.method, tt.platform)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestPlatformsForMethod_DoesNotMutateValidPlatforms(t *testing.T) {
	t.Parallel()
	_ = platformsForMethod("ansible")

	assert.Contains(t, platformsForMethod("token-install"), "suse")
	assert.Contains(t, validPlatforms, "suse")
}

func TestPromptPlatform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		allowed []string
		inputs  []string
		want    string
	}{
		{name: "accepts suse for token-install", allowed: platformsForMethod("token-install"), inputs: []string{"suse"}, want: "suse"},
		{name: "normalizes case and spaces", allowed: platformsForMethod("token-install"), inputs: []string{"  SUSE "}, want: "suse"},
		{name: "asks again after suse is refused for ansible", allowed: platformsForMethod("ansible"), inputs: []string{"suse", "rhel"}, want: "rhel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var prompts []string
			i := 0
			got := promptPlatform(tt.allowed, func(p string) string {
				prompts = append(prompts, p)
				in := tt.inputs[i]
				i++
				return in
			})

			assert.Equal(t, tt.want, got)
			assert.Len(t, prompts, len(tt.inputs))
		})
	}
}
