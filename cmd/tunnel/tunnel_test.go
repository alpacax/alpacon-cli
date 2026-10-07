package tunnel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tunnelapi "github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseTunnelCommandArgs(t *testing.T, rawArgs []string) (*cobra.Command, []string) {
	t.Helper()
	cmd := &cobra.Command{Use: "tunnel"}
	if err := cmd.ParseFlags(rawArgs); err != nil {
		t.Fatalf("failed to parse args %v: %v", rawArgs, err)
	}
	return cmd, cmd.Flags().Args()
}

func TestValidateTunnelArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		rawArgs     []string
		wantErr     bool
		errContains string
	}{
		{
			name:    "tunnel only",
			rawArgs: []string{"prod-db"},
		},
		{
			name:    "tunnel with local command",
			rawArgs: []string{"prod-db", "--", "psql", "-c", "select 1"},
		},
		{
			name:        "missing separator",
			rawArgs:     []string{"prod-db", "psql"},
			wantErr:     true,
			errContains: "missing '--' separator",
		},
		{
			name:        "missing server before separator",
			rawArgs:     []string{"--", "psql"},
			wantErr:     true,
			errContains: "server name is required before '--'",
		},
		{
			name:        "missing command after separator",
			rawArgs:     []string{"prod-db", "--"},
			wantErr:     true,
			errContains: "local command is required after '--'",
		},
		{
			name:        "legacy run subcommand removed",
			rawArgs:     []string{"run", "prod-db", "--", "psql"},
			wantErr:     true,
			errContains: "`alpacon tunnel run` has been removed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := parseTunnelCommandArgs(t, tt.rawArgs)
			err := validateTunnelArgs(cmd, args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %q, want contains %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestExecuteTunnelCommandRunModeReturnsErrorForInvalidRemotePort(t *testing.T) {
	originalFlags := tunnelFlags
	t.Cleanup(func() {
		tunnelFlags = originalFlags
	})

	tunnelFlags.localPort = "5432"
	tunnelFlags.remotePort = "invalid"

	cmd, args := parseTunnelCommandArgs(t, []string{"prod-db", "--", "psql"})
	exitCode, err := executeTunnelCommand(cmd, args, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid remote port") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
}

// tunnelCreateError returns a real tunnel create refusal, whose code is only in ErrorCode().
func tunnelCreateError(t *testing.T, code string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "srv-1", "name": "my-server"}]}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"code": %q}`, code)
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := tunnelapi.CreateTunnelSession(ac, "my-server", "", "", 8082, "")
	require.Error(t, err)
	return err
}

func TestHandleTunnelStartError_TokenRefusalAsksForInteractiveLogin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
	}{
		{"token access control refusal", utils.APITokenACLNotAllowed},
		{"token scope refusal", utils.APITokenScopeMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tunnelCreateError(t, tt.code)

			got := handleTunnelStartError(err, "my-server", nil)

			assert.EqualError(t, got, "a token cannot open a tunnel; run 'alpacon login' without -t and try again")
		})
	}
}

func TestHandleTunnelStartError_OtherRefusalPassesThrough(t *testing.T) {
	t.Parallel()
	err := tunnelCreateError(t, utils.WorkSessionRequired)

	got := handleTunnelStartError(err, "my-server", nil)

	assert.Equal(t, err, got)
}
