package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestPlatformRefusal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		platform string
		want     string
	}{
		{name: "token-install accepts suse", method: "token-install", platform: "suse"},
		{name: "token-install accepts debian", method: "token-install", platform: "debian"},
		{name: "ansible accepts rhel", method: "ansible", platform: "rhel"},
		{name: "ansible rejects suse", method: "ansible", platform: "suse", want: `Platform "suse" is not supported with the ansible method. Valid values: debian, rhel, darwin, windows.`},
		{name: "token-install rejects an unknown platform", method: "token-install", platform: "arch", want: `Invalid platform "arch". Valid values: debian, rhel, suse, darwin, windows.`},
		{name: "ansible rejects an empty platform", method: "ansible", platform: "", want: `Invalid platform "". Valid values: debian, rhel, darwin, windows.`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, platformRefusal(tt.method, tt.platform))
		})
	}
}

func TestFlagRefusal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		interactive bool
		want        string
	}{
		{name: "interactive with no flags leaves both choices to the prompts", interactive: true},
		{name: "interactive suse waits for the method prompt", args: []string{"-p", "suse"}, interactive: true},
		{name: "interactive unknown platform is refused", args: []string{"-p", "arch"}, interactive: true, want: `Invalid platform "arch". Valid values: debian, rhel, suse, darwin, windows.`},
		{name: "ansible with suse is refused", args: []string{"-m", "ansible", "-p", "suse"}, interactive: true, want: `Platform "suse" is not supported with the ansible method. Valid values: debian, rhel, darwin, windows.`},
		{name: "non-interactive suse defaults to token-install", args: []string{"-p", "suse"}},
		{name: "unknown method is refused", args: []string{"-m", "puppet"}, want: `Invalid method "puppet". Valid values: token-install, ansible.`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{Use: "create"}
			cmd.Flags().StringP("method", "m", "token-install", "")
			cmd.Flags().StringP("platform", "p", "", "")
			require.NoError(t, cmd.Flags().Parse(tt.args))

			got := flagRefusal(cmd, tt.interactive)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPromptPlatform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		allowed []string
		inputs  []string
		want    string
	}{
		{name: "accepts suse for token-install", allowed: validPlatforms, inputs: []string{"suse"}, want: "suse"},
		{name: "normalizes case and spaces", allowed: validPlatforms, inputs: []string{"  SUSE "}, want: "suse"},
		{name: "asks again after suse is refused for ansible", allowed: ansiblePlatforms, inputs: []string{"suse", "rhel"}, want: "rhel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var prompts []string
			i := 0
			got := promptPlatform(tt.allowed, strings.Join(tt.allowed, ", "), func(p string) string {
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

const serverCreateHelperMarker = "--server-create-helper--"

func TestServerCreateHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SERVER_CREATE_HELPER") != "1" {
		return
	}
	i := slices.Index(os.Args, serverCreateHelperMarker)
	if i < 0 {
		t.Fatal("missing " + serverCreateHelperMarker + " marker")
	}
	if err := serverCreateCmd.ParseFlags(os.Args[i+1:]); err != nil {
		t.Fatal(err)
	}
	serverCreateCmd.Run(serverCreateCmd, nil)
}

// An expired access token makes NewAlpaconAPIClient refresh over the network,
// so a pair the CLI can reject on its own must be rejected before that.
func TestServerCreate_RejectsAnsibleSuseBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	home := t.TempDir()
	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	cfg, err := json.Marshal(map[string]any{
		"workspace_url":           ts.URL,
		"workspace_name":          "test",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), cfg, 0600))

	helper := osexec.Command(os.Args[0], "-test.run=^TestServerCreateHelperProcess$", "--",
		serverCreateHelperMarker, "-m", "ansible", "-p", "suse", "-t", "prod-token")
	helper.Env = append(os.Environ(), "GO_WANT_SERVER_CREATE_HELPER=1", "HOME="+home, "USERPROFILE="+home)
	var stderr bytes.Buffer
	helper.Stderr = &stderr

	err = helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, stderr.String(), `"suse" is not supported with the ansible method`)
	assert.Zero(t, requests.Load())
}
