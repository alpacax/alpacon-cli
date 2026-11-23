package tunnel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tunnelapi "github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	tunnelruntime "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
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
	t.Parallel()
	flags := tunnelFlagValues{localPort: "5432", remotePort: "invalid"}

	cmd, args := parseTunnelCommandArgs(t, []string{"prod-db", "--", "psql"})
	exitCode, err := executeTunnelCommand(utils.Stdio, &client.AlpaconClient{}, flags, cmd, args, nil)
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
		{"interactive channel refusal", utils.AuthInteractiveChannelRequiresPresence},
		{"MFA presence refusal", utils.AuthMFARequiresHumanPrincipal},
		{"token access control refusal from an older server", utils.APITokenACLNotAllowed},
		{"token scope refusal", utils.APITokenScopeMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tunnelCreateError(t, tt.code)

			got := handleTunnelStartError(&client.AlpaconClient{}, tunnelFlagValues{}, err, "my-server", nil)

			assert.EqualError(t, got, "a token cannot open a tunnel; run 'alpacon login' without -t (on a machine without a browser, open the printed URL on another device) and try again")
		})
	}
}

func TestHandleTunnelStartError_NonGateRefusalPassesThrough(t *testing.T) {
	t.Parallel()
	err := tunnelCreateError(t, "some_other_refusal")

	got := handleTunnelStartError(&client.AlpaconClient{}, tunnelFlagValues{}, err, "my-server", nil)

	assert.Equal(t, err, got)
}

func TestHandleTunnelStartError_CodeQuotedInAgentReportIsNotAGateRefusal(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("failed to open tunnel on my-server: connection refused; code: %s", utils.WorkSessionRequired)

	got := handleTunnelStartError(&client.AlpaconClient{}, tunnelFlagValues{}, err, "my-server", nil)

	assert.Equal(t, err, got)
}

const tunnelGateSessionID = "550e8400-e29b-41d4-a716-446655440000"

// Serial: it sets utils.OutputFormat.
func TestHandleTunnelStartError_GateRefusalExitsThreeWithNextActions(t *testing.T) {
	t.Setenv("AI_AGENT", "")
	utils.OutputFormat = utils.OutputFormatJSON
	t.Cleanup(func() { utils.OutputFormat = utils.OutputFormatTable })
	tests := []struct {
		name string
		code string
	}{
		{"no session selected", utils.WorkSessionRequired},
		{"session not active", utils.WorkSessionNotActive},
		{"server not in session", utils.WorkSessionServerNotAllowed},
		{"session expired", utils.WorkSessionExpired},
		{"account not approved", utils.WorkSessionAccountNotApproved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := tunnelFlagValues{sel: worksession.Fixed(tunnelGateSessionID), authMethod: "Browser login"}
			err := tunnelCreateError(t, tt.code)

			got := handleTunnelStartError(&client.AlpaconClient{}, flags, err, "my-server", nil)

			exitErr, ok := errors.AsType[*utils.ExitError](got)
			require.True(t, ok, "want an ExitError, got %v", got)
			assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitErr.Code)
			var stderr bytes.Buffer
			utils.FprintExitError(&stderr, exitErr)
			var env struct {
				ExitCode  int    `json:"exit_code"`
				ErrorCode string `json:"error_code"`
				Context   struct {
					RequiredScope string   `json:"required_scope"`
					Servers       []string `json:"target_servers"`
					Current       *string  `json:"current_worksession"`
					AuthMethod    string   `json:"auth_method"`
				} `json:"context"`
				NextActions []utils.NextAction `json:"next_actions"`
			}
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &env), stderr.String())
			assert.Equal(t, tt.code, env.ErrorCode)
			assert.Equal(t, "tunnel", env.Context.RequiredScope)
			assert.Equal(t, []string{"my-server"}, env.Context.Servers)
			assert.Equal(t, utils.ExitCodeWorkSessionDenied, env.ExitCode)
			assert.Equal(t, "Browser login", env.Context.AuthMethod)
			require.NotNil(t, env.Context.Current)
			assert.Equal(t, tunnelGateSessionID, *env.Context.Current)
			var commands []string
			for _, a := range env.NextActions {
				commands = append(commands, a.Command)
			}
			assert.Contains(t, strings.Join(commands, "\n"), "--scope tunnel --server my-server")
			assert.NotContains(t, strings.Join(commands, "\n"), "alpacon exec")
		})
	}
}

func TestToStartOptionsCarriesTheCallersClient(t *testing.T) {
	t.Parallel()
	ac := &client.AlpaconClient{WorkspaceName: "ws-a"}

	opts := tunnelFlagValues{remotePort: "5432", sel: worksession.Fixed("")}.toStartOptions(ac, "my-db")

	assert.Same(t, ac, opts.Client)
}

func TestPlainTunnelHandlesSignalsOnlyAfterRecoveryAndClosesCleanly(t *testing.T) {
	runtime := newFakeRunRuntime()
	originalStart, originalSignals := startTunnel, tunnelSignals
	t.Cleanup(func() { startTunnel, tunnelSignals = originalStart, originalSignals })
	var started atomic.Bool
	startTunnel = func(tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		started.Store(true)
		return runtime, nil
	}
	signalChannel := make(chan os.Signal, 1)
	tunnelSignals = func() (<-chan os.Signal, func()) {
		require.True(t, started.Load(), "signal handling must begin after tunnel creation and recovery")
		signalChannel <- os.Interrupt
		return signalChannel, func() {}
	}
	ios, _, stderr := utils.TestIOStreams()
	flags := tunnelFlagValues{sel: worksession.Fixed("session-1")}

	err := executeTunnel(ios, &client.AlpaconClient{}, flags, "prod-db", nil)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, runtime.closeCalls.Load(), int32(1))
	assert.Contains(t, stderr.String(), "Tunnel closed")
}

type tunnelOfferError struct{ code string }

func (e tunnelOfferError) Error() string       { return e.code }
func (e tunnelOfferError) ErrorCode() string   { return e.code }
func (e tunnelOfferError) ErrorSource() string { return "tunnel" }

func TestTunnelOfferResendsWithCreatedTunnelSession(t *testing.T) {
	var createdScopes []string
	active := func() map[string]any {
		return map[string]any{
			"id": "new-session", "status": "active", "requester_type": "user", "scopes": createdScopes,
			"servers": []map[string]string{{"id": "server-1", "name": "prod"}}, "clusters": []any{},
			"assigned_user": map[string]string{"id": "user-1"}, "expires_at": time.Now().Add(time.Hour).UTC(),
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"prod"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			var body struct {
				Scopes []string `json:"scopes"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			createdScopes = body.Scopes
			if !assert.NoError(t, json.NewEncoder(w).Encode(active())) {
				return
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/new-session/":
			if !assert.NoError(t, json.NewEncoder(w).Encode(active())) {
				return
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(server.URL, "ws", "", "", "access-token", "refresh-token", "", 3600, false))
	read, write, err := os.Pipe()
	require.NoError(t, err)
	_, err = write.WriteString("y\npurpose\n1h\n")
	require.NoError(t, err)
	require.NoError(t, write.Close())
	originalStdin := os.Stdin
	os.Stdin = read
	t.Cleanup(func() { os.Stdin = originalStdin; _ = read.Close() })
	ac := &client.AlpaconClient{BaseURL: server.URL, HTTPClient: server.Client(), WorkspaceName: "ws"}
	ac.SetAccessToken("access-token")
	originalSelect, originalStart := selectTunnelWorkSession, startTunnel
	t.Cleanup(func() { selectTunnelWorkSession, startTunnel = originalSelect, originalStart })
	selectTunnelWorkSession = func(ios *utils.IOStreams, ac *client.AlpaconClient, _ string, need worksession.Need) (*worksession.Selection, error) {
		assert.Equal(t, worksession.Tunnel, need.Operation)
		return worksession.OfferedForTest(ios, ac, "old-session", need), nil
	}
	var ids []string
	startTunnel = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		ids = append(ids, opts.WorkSessionID)
		if len(ids) == 1 {
			return nil, utils.MarkSubmission(tunnelOfferError{code: utils.WorkSessionRequired})
		}
		return nil, errors.New("retry observed")
	}
	ios, _, _ := utils.TestIOStreams()

	err = runTunnel(ios, &cobra.Command{}, []string{"prod"}, tunnelFlagValues{localPort: "0", remotePort: "5432"}, func() (*client.AlpaconClient, error) { return ac, nil })

	require.Error(t, err)
	assert.Equal(t, []string{"old-session", "new-session"}, ids)
	assert.Equal(t, []string{utils.OperationTunnel}, createdScopes)
}

func TestTunnelOfferIsInterruptibleInBothModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send os.Interrupt to a process")
	}
	for _, mode := range []string{"plain", "command"} {
		for _, tc := range []struct{ name, input, want string }{
			{name: "prompt", want: "exit status 3"},
			{name: "wait", input: "y\npurpose\n1h\n", want: "exit status 4"},
		} {
			t.Run(mode+" "+tc.name, func(t *testing.T) {
				child := osexec.Command(os.Args[0], "-test.run=^TestTunnelOfferInterruptHelper$", "--", mode, tc.name)
				child.Env = append(os.Environ(), "HOME="+t.TempDir())
				stdin, err := child.StdinPipe()
				require.NoError(t, err)
				stdout, err := child.StdoutPipe()
				require.NoError(t, err)
				require.NoError(t, child.Start())
				t.Cleanup(func() { _ = child.Process.Kill() })
				_, err = stdin.Write([]byte(tc.input))
				require.NoError(t, err)
				scanner := bufio.NewScanner(stdout)
				require.True(t, scanner.Scan(), "helper must reach the offer")
				require.Equal(t, "ready", scanner.Text())

				require.NoError(t, child.Process.Signal(os.Interrupt))

				done := make(chan error, 1)
				go func() { done <- child.Wait() }()
				select {
				case waitErr := <-done:
					require.EqualError(t, waitErr, tc.want)
				case <-time.After(5 * time.Second):
					t.Fatal("Ctrl+C did not stop the offer")
				}
			})
		}
	}
}

// Serial: the child process exits or dies by the signal, and the parent's own run returns at once.
func TestTunnelOfferInterruptHelper(t *testing.T) {
	dash := slices.Index(os.Args, "--")
	if dash < 0 || len(os.Args) != dash+3 {
		return
	}
	mode, stage := os.Args[dash+1], os.Args[dash+2]
	pending := `{"id":"new-session","status":"pending","requester_type":"user"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ready := ""
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"user-1"}`))
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"prod"}]}`))
		case r.URL.Path == "/api/work-sessions/sessions/" && r.Method == http.MethodGet:
			ready = "prompt"
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(pending))
		default:
			ready = "wait"
			_, _ = w.Write([]byte(pending))
		}
		if ready == stage {
			_, _ = fmt.Fprintln(os.Stdout, "ready")
		}
	}))
	defer server.Close()
	ac := &client.AlpaconClient{BaseURL: server.URL, HTTPClient: server.Client(), WorkspaceName: "ws"}
	selectTunnelWorkSession = func(ios *utils.IOStreams, ac *client.AlpaconClient, _ string, need worksession.Need) (*worksession.Selection, error) {
		return worksession.OfferedForTest(ios, ac, "old-session", need), nil
	}
	startTunnel = func(tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return nil, utils.MarkSubmission(tunnelOfferError{code: utils.WorkSessionRequired})
	}
	cmd := &cobra.Command{}
	args := []string{"prod"}
	if mode == "command" {
		cmd, args = parseTunnelCommandArgs(t, []string{"prod", "--", "true"})
	}

	err := runTunnel(utils.Stdio, cmd, args, tunnelFlagValues{localPort: "0", remotePort: "5432"}, func() (*client.AlpaconClient, error) { return ac, nil })

	var exitErr *utils.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}
	os.Exit(99)
}
