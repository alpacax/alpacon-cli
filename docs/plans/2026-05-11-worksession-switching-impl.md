# Work-session switching implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `alpacon work-session use <UUID>` (per-workspace persistent active session) and `--work-session` flag on `exec`/`websh`/`cp`/`tunnel` that overrides config; flag > config priority, no env var.

**Architecture:** UUID resolved in `cmd/` layer via `worksession.Resolve(flag)`, passed as explicit `workSessionID string` parameter into each API function, conditionally injected into the request body JSON as `work_session` field (DRF serializer field, allow_null=True). Active session persisted per workspace in `~/.alpacon/config.json` under `active_work_sessions: {workspace_name: uuid}`. `use <UUID>` validates via `GET /api/work-sessions/<UUID>/` before saving (status must be ACTIVE).

**Tech Stack:** Go 1.25.7, Cobra, testify/assert, net/http/httptest. Server contract (alpacon-server) already supports `work_session` body field for events/websh/tunnel; `ftp` server-side support is a cross-repo dependency tracked separately.

**Reference spec:** [`docs/plans/2026-05-11-worksession-switching.md`](2026-05-11-worksession-switching.md)

---

## File map

| File | Action | Responsibility |
|---|---|---|
| `config/types.go` | Modify | Add `ActiveWorkSessions map[string]string` field |
| `config/config.go` | Modify | Add `SetActiveWorkSession`, `GetActiveWorkSession` helpers |
| `config/config_test.go` | Modify | Tests for per-workspace isolation, unset, legacy load |
| `cmd/worksession/resolve.go` | Create | `Resolve(flagValue)`, `AnnounceIfActive(uuid)` |
| `cmd/worksession/resolve_test.go` | Create | Priority table tests |
| `cmd/worksession/worksession_use.go` | Create | `work-session use <UUID> [--unset]` command |
| `cmd/worksession/worksession_use_test.go` | Create | Validation flow tests |
| `cmd/worksession/worksession_current.go` | Create | `work-session current` command |
| `cmd/worksession/worksession_current_test.go` | Create | Current command tests |
| `cmd/worksession/worksession.go` | Modify | Register `use` and `current` subcommands |
| `cmd/worksession/worksession_list.go` | Modify | Add ` * ` marker for active row in table output |
| `cmd/worksession/worksession_list_test.go` | Modify (if exists) | Marker tests |
| `api/event/types.go` | Modify | Add `WorkSession *string` to `CommandRequest` |
| `api/event/event.go` | Modify | `RunCommand` gains `workSessionID` param |
| `api/event/event_test.go` | Create or modify | Body assertion for work_session field |
| `api/websh/types.go` | Modify | Add `WorkSession *string` to `SessionRequest` |
| `api/websh/websh.go` | Modify | `CreateWebshSession` gains `workSessionID` param |
| `api/websh/websh_test.go` | Create or modify | Body assertion |
| `api/tunnel/types.go` | Modify | Add `WorkSession *string` to `TunnelSessionRequest` |
| `api/tunnel/tunnel.go` | Modify | `CreateTunnelSession` gains `workSessionID` param |
| `api/tunnel/tunnel_test.go` | Create or modify | Body assertion |
| `api/ftp/ftp.go` | Modify | `UploadFile`/`UploadFolder`/`DownloadFile` gain `workSessionID` |
| `api/ftp/ftp_test.go` | Create or modify | Body assertion |
| `pkg/tunnel/runtime/runtime.go` | Modify | `StartOptions` gains `WorkSessionID`, passes to `CreateTunnelSession` |
| `cmd/exec/exec.go` | Modify | Doc string only |
| `cmd/exec/parse.go` | Modify | Manual parser handles `--work-session` |
| `cmd/exec/parse_test.go` | Modify | Parser tests |
| `cmd/exec/run.go` | Modify | `RunCommandWithRetry` gains `workSessionID` param |
| `cmd/websh/websh.go` | Modify | Manual parser + pass to `CreateWebshSession`/`RunCommandWithRetry` |
| `cmd/websh/websh_test.go` | Modify | Parser tests |
| `cmd/ftp/cp.go` | Modify | Standard cobra flag + pipe through |
| `cmd/tunnel/tunnel.go` | Modify | Standard cobra flag + plumb into `StartOptions` |

Total: 8 new files, ~15 modified files.

---

## Task 1: Config — ActiveWorkSessions field

**Files:**
- Modify: `config/types.go`

- [ ] **Step 1: Write the failing test for legacy config load (no `active_work_sessions` key)**

Append to `config/config_test.go`:

```go
func TestLoadConfig_LegacyWithoutActiveWorkSessions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfgDir := filepath.Join(tmpHome, config.ConfigFileDir)
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	legacy := `{"workspace_url":"https://ws.example.com","workspace_name":"ws-a"}`
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName), []byte(legacy), 0600))

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.ActiveWorkSessions)
}
```

Imports to add to `config/config_test.go` if missing: `os`, `path/filepath`, `github.com/stretchr/testify/require`. (Existing tests already use `testify`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./config/ -run TestLoadConfig_LegacyWithoutActiveWorkSessions -v`
Expected: FAIL with `cfg.ActiveWorkSessions undefined` compile error.

- [ ] **Step 3: Add field to Config**

In `config/types.go`, modify the struct to add the field after `Insecure`:

```go
type Config struct {
	WorkspaceURL         string            `json:"workspace_url"`
	WorkspaceName        string            `json:"workspace_name"`
	Token                string            `json:"token,omitempty"`
	ExpiresAt            string            `json:"expires_at,omitempty"`
	AccessToken          string            `json:"access_token,omitempty"`
	RefreshToken         string            `json:"refresh_token,omitempty"`
	AccessTokenExpiresAt string            `json:"access_token_expires_at,omitempty"`
	BaseDomain           string            `json:"base_domain,omitempty"`
	Insecure             bool              `json:"insecure"`
	ActiveWorkSessions   map[string]string `json:"active_work_sessions,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./config/ -run TestLoadConfig_LegacyWithoutActiveWorkSessions -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/types.go config/config_test.go
git commit -m "feat(config): add ActiveWorkSessions field to Config"
```

---

## Task 2: Config — Set/GetActiveWorkSession helpers

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

- [ ] **Step 1: Write failing tests for round-trip, unset, per-workspace isolation**

Append to `config/config_test.go`:

```go
func TestActiveWorkSession_RoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	require.NoError(t, config.CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))

	require.NoError(t, config.SetActiveWorkSession("uuid-1"))
	got, err := config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-1", got)
}

func TestActiveWorkSession_UnsetRemovesKey(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	require.NoError(t, config.CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, config.SetActiveWorkSession("uuid-1"))
	require.NoError(t, config.SetActiveWorkSession(""))

	got, err := config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "", got)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	_, exists := cfg.ActiveWorkSessions["ws-a"]
	assert.False(t, exists, "key should be removed from map on unset")
}

func TestActiveWorkSession_PerWorkspaceIsolation(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	require.NoError(t, config.CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, config.SetActiveWorkSession("uuid-A"))

	require.NoError(t, config.SwitchWorkspace("https://ws-b.example.com", "ws-b"))
	got, err := config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "", got, "switching workspace should yield empty active session for new workspace")

	require.NoError(t, config.SetActiveWorkSession("uuid-B"))

	require.NoError(t, config.SwitchWorkspace("https://ws-a.example.com", "ws-a"))
	got, err = config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-A", got, "switching back should restore original active session")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./config/ -run TestActiveWorkSession -v`
Expected: FAIL with `config.SetActiveWorkSession undefined`.

- [ ] **Step 3: Add helpers to `config/config.go`**

Append at the end of `config/config.go` (before `GetSmuxConfig`):

```go
// SetActiveWorkSession persists the work-session UUID for the current workspace.
// Pass "" to clear the entry for the current workspace.
func SetActiveWorkSession(uuid string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}
	if cfg.WorkspaceName == "" {
		return fmt.Errorf("no active workspace; run 'alpacon login' first")
	}
	if cfg.ActiveWorkSessions == nil {
		cfg.ActiveWorkSessions = map[string]string{}
	}
	if uuid == "" {
		delete(cfg.ActiveWorkSessions, cfg.WorkspaceName)
	} else {
		cfg.ActiveWorkSessions[cfg.WorkspaceName] = uuid
	}
	return saveConfig(&cfg)
}

// GetActiveWorkSession returns the active work-session UUID for the current workspace.
// Returns "" (no error) when no session is set or the config is missing the map.
func GetActiveWorkSession() (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	if cfg.ActiveWorkSessions == nil {
		return "", nil
	}
	return cfg.ActiveWorkSessions[cfg.WorkspaceName], nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config/ -v`
Expected: All tests PASS (including the new ones and any pre-existing).

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): add Set/GetActiveWorkSession helpers"
```

---

## Task 3: Resolver — `cmd/worksession/resolve.go`

**Files:**
- Create: `cmd/worksession/resolve.go`
- Create: `cmd/worksession/resolve_test.go`

- [ ] **Step 1: Write failing tests**

Create `cmd/worksession/resolve_test.go`:

```go
package worksession_test

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_Priority(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		cfgUUID  string
		expected string
	}{
		{"both empty", "", "", ""},
		{"only config", "", "uuid-cfg", "uuid-cfg"},
		{"only flag", "uuid-flag", "", "uuid-flag"},
		{"flag wins over config", "uuid-flag", "uuid-cfg", "uuid-flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)
			require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
			if tt.cfgUUID != "" {
				require.NoError(t, config.SetActiveWorkSession(tt.cfgUUID))
			}

			got, err := worksession.Resolve(tt.flag)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestAnnounceIfActive_PrintsToStderr(t *testing.T) {
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	worksession.AnnounceIfActive("uuid-x")
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	assert.Contains(t, buf.String(), "Using work-session uuid-x")
}

func TestAnnounceIfActive_SilentWhenEmpty(t *testing.T) {
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	worksession.AnnounceIfActive("")
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	assert.Equal(t, "", buf.String())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/worksession/ -run "Resolve|Announce" -v`
Expected: FAIL with `worksession.Resolve undefined`.

- [ ] **Step 3: Create resolver**

Create `cmd/worksession/resolve.go`:

```go
package worksession

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/config"
)

// Resolve returns the work-session UUID to use for an operation.
// Precedence: flag > config. Returns "" when nothing is set.
func Resolve(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return config.GetActiveWorkSession()
}

// AnnounceIfActive prints "Using work-session <uuid>" to stderr when uuid != "".
// Stderr keeps stdout clean for --output json consumers and shell pipelines.
func AnnounceIfActive(uuid string) {
	if uuid == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "Using work-session %s\n", uuid)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/worksession/ -run "Resolve|Announce" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/worksession/resolve.go cmd/worksession/resolve_test.go
git commit -m "feat(worksession): add Resolve and AnnounceIfActive helpers"
```

---

## Task 4: `work-session use` command

**Files:**
- Create: `cmd/worksession/worksession_use.go`
- Create: `cmd/worksession/worksession_use_test.go`
- Modify: `cmd/worksession/worksession.go`

The `use` command calls existing `wsapi.GetWorkSession(ac, uuid)` to validate, then `config.SetActiveWorkSession(uuid)`. To keep tests hermetic, factor a unit-testable helper that takes a client and writes to config; the cobra `Run` wires the real client.

- [ ] **Step 1: Check the WorkSession response shape**

Run: `grep -n "type WorkSession " api/worksession/*.go`
Expected output should include a `Status string` and `Name string` (or similar) field. Read `api/worksession/types.go` if needed to confirm exact field names before writing tests.

Note the actual field names (e.g. `Status`, `Name`) for use in Step 2/3. The plan below assumes `Status` and `Name`; substitute if different.

- [ ] **Step 2: Write failing tests**

Create `cmd/worksession/worksession_use_test.go`:

```go
package worksession_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: &http.Client{},
		BaseURL:    ts.URL,
	}
}

func setupTmpConfig(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
}

func TestRunUse_Success_PersistsToConfig(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/work-sessions/ses-abc/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "ses-abc",
			"name":   "incident-response",
			"status": "ACTIVE",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	name, err := worksession.RunUse(ac, "ses-abc")
	require.NoError(t, err)
	assert.Equal(t, "incident-response", name)

	got, err := config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got)
}

func TestRunUse_NotFound(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	_, err := worksession.RunUse(ac, "ses-missing")
	require.Error(t, err)

	got, _ := config.GetActiveWorkSession()
	assert.Equal(t, "", got, "config must not be updated on failure")
}

func TestRunUse_RejectsNonActiveStatus(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "ses-pending",
			"name":   "queue",
			"status": "PENDING_APPROVAL",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	_, err := worksession.RunUse(ac, "ses-pending")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PENDING_APPROVAL")

	got, _ := config.GetActiveWorkSession()
	assert.Equal(t, "", got)
}

func TestRunUnset_Idempotent(t *testing.T) {
	setupTmpConfig(t)

	// First call on empty
	err := worksession.RunUnset()
	require.NoError(t, err)

	// Set something, unset, verify clear
	require.NoError(t, config.SetActiveWorkSession("ses-xyz"))
	err = worksession.RunUnset()
	require.NoError(t, err)
	got, _ := config.GetActiveWorkSession()
	assert.Equal(t, "", got)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./cmd/worksession/ -run "RunUse|RunUnset" -v`
Expected: FAIL with `worksession.RunUse undefined`.

- [ ] **Step 4: Implement `cmd/worksession/worksession_use.go`**

Create `cmd/worksession/worksession_use.go`:

```go
package worksession

import (
	"errors"
	"fmt"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var unsetActiveWorkSession bool

// RunUse validates the work-session via the server, then stores it in config.
// Returns the human-readable session name on success.
func RunUse(ac *client.AlpaconClient, uuid string) (string, error) {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return "", err
	}
	if ws == nil {
		return "", fmt.Errorf("work session not found: %s", uuid)
	}
	if ws.Status != "ACTIVE" {
		return "", fmt.Errorf("work session %s is in '%s' state and cannot be used", uuid, ws.Status)
	}
	if err := config.SetActiveWorkSession(uuid); err != nil {
		return "", err
	}
	return ws.Name, nil
}

// RunUnset clears the active work-session for the current workspace.
// Idempotent — no error when nothing is set.
func RunUnset() error {
	return config.SetActiveWorkSession("")
}

var workSessionUseCmd = &cobra.Command{
	Use:   "use [UUID]",
	Short: "Set or clear the active work-session for the current workspace",
	Long: `Set the active work-session UUID for the current workspace.
Subsequent exec/websh/cp/tunnel commands attach to this session unless overridden with --work-session.
Use --unset to clear.`,
	Example: `  alpacon work-session use ses-abc123
  alpacon work-session use --unset`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if unsetActiveWorkSession {
			if len(args) > 0 {
				return errors.New("--unset cannot be combined with a UUID argument")
			}
			cur, _ := config.GetActiveWorkSession()
			if err := RunUnset(); err != nil {
				return err
			}
			if cur == "" {
				utils.CliInfo("No active work-session to unset.")
			} else {
				utils.CliSuccess("Active work-session cleared.")
			}
			return nil
		}

		if len(args) != 1 {
			return errors.New("UUID argument is required (or pass --unset)")
		}
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			return fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
		}
		name, err := RunUse(ac, args[0])
		if err != nil {
			return err
		}
		utils.CliSuccess("Active work-session set to %s (%s).", args[0], name)
		return nil
	},
}

func init() {
	workSessionUseCmd.Flags().BoolVar(&unsetActiveWorkSession, "unset", false, "Clear the active work-session for the current workspace")
}
```

- [ ] **Step 5: Register subcommand in `cmd/worksession/worksession.go`**

In `cmd/worksession/worksession.go` `init()` function, add the new command and update the error string:

```go
func init() {
	WorkSessionCmd.AddCommand(workSessionListCmd)
	WorkSessionCmd.AddCommand(workSessionCreateCmd)
	WorkSessionCmd.AddCommand(workSessionDescribeCmd)
	WorkSessionCmd.AddCommand(workSessionActivateCmd)
	WorkSessionCmd.AddCommand(workSessionCompleteCmd)
	WorkSessionCmd.AddCommand(workSessionExtendCmd)
	WorkSessionCmd.AddCommand(workSessionUseCmd)
}
```

Update the `RunE` error message in the same file:

```go
return errors.New("a subcommand is required. Use 'alpacon work-session ls', 'alpacon work-session create', 'alpacon work-session describe', 'alpacon work-session use', 'alpacon work-session activate', 'alpacon work-session complete', or 'alpacon work-session extend'. Run 'alpacon work-session --help' for more information")
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/worksession/ -v`
Expected: PASS.

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add cmd/worksession/worksession_use.go cmd/worksession/worksession_use_test.go cmd/worksession/worksession.go
git commit -m "feat(worksession): add 'use' subcommand with --unset"
```

---

## Task 5: `work-session current` command

**Files:**
- Create: `cmd/worksession/worksession_current.go`
- Create: `cmd/worksession/worksession_current_test.go`
- Modify: `cmd/worksession/worksession.go` (register)

- [ ] **Step 1: Write failing tests**

Create `cmd/worksession/worksession_current_test.go`:

```go
package worksession_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCurrent_NoActive_ReturnsEmpty(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called when no active session")
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := worksession.RunCurrent(ac)
	require.NoError(t, err)
	assert.Equal(t, "", uuid)
	assert.Nil(t, ws)
}

func TestRunCurrent_ActiveResolves(t *testing.T) {
	setupTmpConfig(t)
	require.NoError(t, config.SetActiveWorkSession("ses-abc"))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "ses-abc",
			"name":   "incident-response",
			"status": "ACTIVE",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := worksession.RunCurrent(ac)
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", uuid)
	require.NotNil(t, ws)
	assert.Equal(t, "incident-response", ws.Name)
}

func TestRunCurrent_StaleUUID_ServerNotFound(t *testing.T) {
	setupTmpConfig(t)
	require.NoError(t, config.SetActiveWorkSession("ses-stale"))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := worksession.RunCurrent(ac)
	require.Error(t, err)
	assert.Equal(t, "ses-stale", uuid)
	assert.Nil(t, ws)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/worksession/ -run "RunCurrent" -v`
Expected: FAIL with `worksession.RunCurrent undefined`.

- [ ] **Step 3: Implement `cmd/worksession/worksession_current.go`**

Create `cmd/worksession/worksession_current.go`:

```go
package worksession

import (
	"encoding/json"
	"fmt"
	"os"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// RunCurrent returns the active work-session UUID and the fetched session detail
// for the current workspace. Returns ("", nil, nil) when nothing is set.
// Returns (uuid, nil, err) when the UUID is set but the server cannot resolve it.
func RunCurrent(ac *client.AlpaconClient) (string, *wsapi.WorkSession, error) {
	uuid, err := config.GetActiveWorkSession()
	if err != nil {
		return "", nil, err
	}
	if uuid == "" {
		return "", nil, nil
	}
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return uuid, nil, err
	}
	return uuid, ws, nil
}

var workSessionCurrentCmd = &cobra.Command{
	Use:     "current",
	Short:   "Show the active work-session for the current workspace",
	Example: `  alpacon work-session current`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			return fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
		}
		uuid, ws, err := RunCurrent(ac)
		if err != nil {
			if uuid != "" {
				return fmt.Errorf("active work-session %s no longer accessible: %w. Run 'alpacon work-session use --unset' to clear", uuid, err)
			}
			return err
		}
		if uuid == "" {
			if utils.OutputFormat == "json" {
				_, _ = fmt.Fprintln(os.Stdout, "null")
			} else {
				utils.CliInfo("No active work-session.")
			}
			return nil
		}
		if utils.OutputFormat == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(ws)
		}
		utils.PrintTable([]*wsapi.WorkSession{ws})
		return nil
	},
}
```

- [ ] **Step 4: Register in `cmd/worksession/worksession.go`**

Add inside `init()`:

```go
	WorkSessionCmd.AddCommand(workSessionCurrentCmd)
```

Also update the RunE error message to mention `current`:

```go
return errors.New("a subcommand is required. Use 'alpacon work-session ls', 'alpacon work-session create', 'alpacon work-session describe', 'alpacon work-session use', 'alpacon work-session current', 'alpacon work-session activate', 'alpacon work-session complete', or 'alpacon work-session extend'. Run 'alpacon work-session --help' for more information")
```

- [ ] **Step 5: Run tests + build**

```bash
go test ./cmd/worksession/ -v
go build ./...
```

Expected: PASS, no build errors.

Note: `utils.PrintTable` may require a specific slice/struct shape — if test of cobra Run is needed, gate it behind a thin runner. The unit tests in Step 1 cover `RunCurrent` (the logic), not the cobra Run body.

- [ ] **Step 6: Commit**

```bash
git add cmd/worksession/worksession_current.go cmd/worksession/worksession_current_test.go cmd/worksession/worksession.go
git commit -m "feat(worksession): add 'current' subcommand"
```

---

## Task 6: `work-session ls` — active marker

**Files:**
- Modify: `cmd/worksession/worksession_list.go`

The marker requirement: prepend a column (or first-column prefix) showing ` * ` for the row whose ID matches `config.GetActiveWorkSession()`. JSON output unchanged.

- [ ] **Step 1: Read current `worksession_list.go` and identify the projected struct**

Run: `cat cmd/worksession/worksession_list.go`
Locate the struct used by `utils.PrintTable` and the table column tags. Note the field that holds the session UUID and the field used as the leftmost column.

- [ ] **Step 2: Write a failing test**

Append a new test (or modify existing) in `cmd/worksession/worksession_list_test.go`:

```go
func TestProjectListItems_MarksActive(t *testing.T) {
	items := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Name: "alpha", Status: "ACTIVE"},
		{ID: "ses-2", Name: "beta", Status: "ACTIVE"},
	}

	rows := worksession.ProjectListItems(items, "ses-2")

	require.Len(t, rows, 2)
	assert.Equal(t, "", rows[0].Active, "non-active row has no marker")
	assert.Equal(t, "*", rows[1].Active, "active row has marker")
}
```

If `worksession_list_test.go` does not exist, create it with the appropriate `package worksession_test` header and imports (`testing`, `assert`, `require`, `wsapi`, `worksession` packages).

- [ ] **Step 3: Run test to verify failure**

Run: `go test ./cmd/worksession/ -run TestProjectListItems_MarksActive -v`
Expected: FAIL with `worksession.ProjectListItems undefined` or related.

- [ ] **Step 4: Refactor list command to use a unit-testable projector**

In `cmd/worksession/worksession_list.go`, extract the row-projection logic into a function:

```go
// ListRow is the table-output projection of a work-session.
type ListRow struct {
	Active string `json:"-" table:" "` // " * " marker column, hidden from JSON
	// ... copy remaining fields from the existing projection struct verbatim ...
}

// ProjectListItems projects API results into table rows, marking the row
// whose ID matches activeUUID with " * " in the Active column.
func ProjectListItems(items []wsapi.WorkSessionAttributes, activeUUID string) []ListRow {
	rows := make([]ListRow, 0, len(items))
	for _, it := range items {
		row := ListRow{
			// ... copy field assignments from the existing projection ...
		}
		if activeUUID != "" && it.ID == activeUUID {
			row.Active = "*"
		}
		rows = append(rows, row)
	}
	return rows
}
```

Replace the inline loop in the cobra `Run` body with a call to `ProjectListItems(results, activeUUID)`, where `activeUUID, _ := config.GetActiveWorkSession()`.

For `--output json`: pass the raw API results (not `ListRow`) to `utils.PrintJson` so the marker doesn't leak into JSON. If the existing code already calls `utils.PrintTable`/`utils.PrintJson` conditionally on `utils.OutputFormat`, retain that branching.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/worksession/ -v`
Expected: PASS.

- [ ] **Step 6: Verify the JSON path is clean**

Run: `go test ./cmd/worksession/ -run TestProjectListItems -v` and inspect that the JSON path uses the raw API type (not `ListRow`) so the `Active` column does not appear in JSON output.

- [ ] **Step 7: Commit**

```bash
git add cmd/worksession/worksession_list.go cmd/worksession/worksession_list_test.go
git commit -m "feat(worksession): mark active session in 'ls' table output"
```

---

## Task 7: API event.RunCommand — workSessionID parameter

**Files:**
- Modify: `api/event/types.go`
- Modify: `api/event/event.go`
- Create or modify: `api/event/event_test.go`

- [ ] **Step 1: Write failing test for body field presence**

Create or append `api/event/event_test.go`:

```go
package event_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommand_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var sawWorkSession string
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/api/events/commands/") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			v, ok := payload["work_session"]
			hadKey = ok
			if ok {
				sawWorkSession, _ = v.(string)
			}
			// Respond minimally; downstream poll is fine to error in this test.
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "cmd-1"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	// PollCommandExecution will fail; we don't care for body assertion.
	_, _ = event.RunCommand(ac, "server-x", "ls", "", "", nil, "ses-abc")

	require.True(t, hadKey, "body must contain work_session field when ID is set")
	assert.Equal(t, "ses-abc", sawWorkSession)
}

func TestRunCommand_BodyOmitsWorkSession_WhenEmpty(t *testing.T) {
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/api/events/commands/") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			_, hadKey = payload["work_session"]
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "cmd-1"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	_, _ = event.RunCommand(ac, "server-x", "ls", "", "", nil, "")

	assert.False(t, hadKey, "body must omit work_session field when ID is empty")
}
```

Note: `server.GetServerIDByName` is called inside `RunCommand`. The httptest needs to either also serve the server lookup endpoint, or the test must bypass it. The pragmatic fix: extend the handler to also serve `/api/servers/?name=server-x` returning `{"results": [{"id": "srv-1"}]}`. Add that handler branch.

Augment the handler accordingly:

```go
if r.Method == "GET" && strings.Contains(r.URL.Path, "/api/servers/") {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"results": []map[string]any{{"id": "srv-1", "name": "server-x"}},
	})
	return
}
```

If `GetServerIDByName` uses a different path/shape, adjust the handler to match (read `api/server/server.go` to confirm).

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./api/event/ -v`
Expected: FAIL — compile error on the new param.

- [ ] **Step 3: Add field to `CommandRequest`**

In `api/event/types.go`:

```go
type CommandRequest struct {
	Shell       string            `json:"shell"`
	Line        string            `json:"line"`
	Env         map[string]string `json:"env"`
	Data        string            `json:"data"`
	Username    string            `json:"username"`
	Groupname   string            `json:"groupname"`
	ScheduledAt *time.Time        `json:"scheduled_at"`
	Server      string            `json:"server"`
	RunAfter    []string          `json:"run_after"`
	WorkSession string            `json:"work_session,omitempty"`
}
```

- [ ] **Step 4: Update `RunCommand` signature and body construction**

In `api/event/event.go`, modify `RunCommand`:

```go
func RunCommand(ac *client.AlpaconClient, serverName, command string, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return "", err
	}

	commandRequest := &CommandRequest{
		Shell:       "system",
		Line:        command,
		Env:         env,
		Username:    username,
		Groupname:   groupname,
		Server:      serverID,
		RunAfter:    []string{},
		WorkSession: workSessionID,
	}
	respBody, err := ac.SendPostRequest(getEventURL, commandRequest)
	// ... rest unchanged ...
}
```

The `omitempty` on the struct tag ensures the key is absent when `workSessionID == ""`.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./api/event/ -v`
Expected: PASS (both new tests).

- [ ] **Step 6: Don't commit yet** — all callers of `RunCommand` need updating in the next task to keep the build green.

---

## Task 8: cmd/exec wiring — `--work-session` flag + RunCommandWithRetry

**Files:**
- Modify: `cmd/exec/run.go`
- Modify: `cmd/exec/parse.go`
- Modify: `cmd/exec/parse_test.go`
- Modify: `cmd/exec/exec.go`

Since `exec` uses `DisableFlagParsing: true`, `--work-session` must be added to the manual parser in `parse.go`.

- [ ] **Step 1: Read current parser**

Run: `grep -n "WorkSession\|--username\|-u " cmd/exec/parse.go` to see how existing flags are parsed.

- [ ] **Step 2: Add failing test in `cmd/exec/parse_test.go`**

```go
func TestParseRemoteExecArgs_WorkSessionFlag(t *testing.T) {
	parsed := exec.ParseRemoteExecArgs([]string{"--work-session", "ses-abc", "my-server", "ls"})
	assert.Equal(t, "ses-abc", parsed.WorkSessionID)
	assert.Equal(t, "my-server", parsed.Server)
	assert.Equal(t, "ls", parsed.Command)
}

func TestParseRemoteExecArgs_WorkSessionEqualForm(t *testing.T) {
	parsed := exec.ParseRemoteExecArgs([]string{"--work-session=ses-abc", "my-server", "ls"})
	assert.Equal(t, "ses-abc", parsed.WorkSessionID)
}

func TestParseRemoteExecArgs_DoubleDashIgnoresWorkSession(t *testing.T) {
	parsed := exec.ParseRemoteExecArgs([]string{"my-server", "--", "ls", "--work-session", "fake"})
	assert.Equal(t, "", parsed.WorkSessionID)
	assert.Equal(t, "my-server", parsed.Server)
	assert.Contains(t, parsed.Command, "--work-session")
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./cmd/exec/ -run "WorkSession" -v`
Expected: FAIL.

- [ ] **Step 4: Add `WorkSessionID` to parse result and extend parser**

In `cmd/exec/parse.go`:
- Add field `WorkSessionID string` to the `ParsedRemoteExecArgs` struct (or whichever struct `ParseRemoteExecArgs` returns)
- In the manual loop, add case alongside `--username`/`-u`:

```go
case args[i] == "--work-session" || strings.HasPrefix(args[i], "--work-session="):
	if strings.Contains(args[i], "=") {
		parsed.WorkSessionID = strings.SplitN(args[i], "=", 2)[1]
	} else if i+1 < len(args) {
		parsed.WorkSessionID = args[i+1]
		i++
	}
```

(Match the exact pattern used by existing flags in the file.)

- [ ] **Step 5: Update `RunCommandWithRetry` signature in `cmd/exec/run.go`**

```go
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	result, err := event.RunCommand(ac, serverName, command, username, groupname, env, workSessionID)
	if err != nil {
		err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
			// ... existing callbacks ...
			RetryOperation: func() error {
				result, err = event.RunCommand(ac, serverName, command, username, groupname, env, workSessionID)
				return err
			},
		})
		// ... rest unchanged ...
	}
	return result, nil
}
```

- [ ] **Step 6: Update `cmd/exec/exec.go` `Run` body**

Inside the cobra `Run` function, after `parsed := ParseRemoteExecArgs(args)` and before `event.RunCommand`:

```go
uuid, err := worksession.Resolve(parsed.WorkSessionID)
if err != nil {
	utils.CliErrorWithExit("%s", err)
	return
}
worksession.AnnounceIfActive(uuid)

env := make(map[string]string)
result, err := RunCommandWithRetry(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, uuid)
```

Add import: `"github.com/alpacax/alpacon-cli/cmd/worksession"`.

Update the cobra `Long` and `Flags` doc strings to mention `--work-session`:

```go
Long: `Execute a command on a remote server.
...
Flags:
  -u, --username [USER_NAME]    Specify the username for command execution.
  -g, --groupname [GROUP_NAME]  Specify the group name for command execution.
  --work-session [UUID]         Attach this command to a work-session.
                                Overrides the workspace's active session set via
                                'alpacon work-session use'.`,
```

- [ ] **Step 7: Run tests + build**

```bash
go test ./cmd/exec/ -v
go build ./...
```

Expected: PASS, build clean.

- [ ] **Step 8: Commit (event API + exec)**

```bash
git add api/event/types.go api/event/event.go api/event/event_test.go cmd/exec/run.go cmd/exec/parse.go cmd/exec/parse_test.go cmd/exec/exec.go
git commit -m "feat(exec): add --work-session flag with config-tier fallback"
```

---

## Task 9: API websh.CreateWebshSession — workSessionID parameter

**Files:**
- Modify: `api/websh/types.go`
- Modify: `api/websh/websh.go`
- Create or modify: `api/websh/websh_test.go`

- [ ] **Step 1: Write failing tests**

Create `api/websh/websh_test.go` (or append):

```go
func TestCreateWebshSession_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var sawValue string
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/api/servers/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
			return
		}
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/api/websh/sessions") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			v, ok := payload["work_session"]
			hadKey = ok
			if ok {
				sawValue, _ = v.(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ws-1", "websocket_url": "wss://x"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	_, _ = websh.CreateWebshSession(ac, "server-x", "", "", false, false, "ses-abc")

	require.True(t, hadKey)
	assert.Equal(t, "ses-abc", sawValue)
}

func TestCreateWebshSession_BodyOmitsWorkSession_WhenEmpty(t *testing.T) {
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		_, hadKey = payload["work_session"]
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ws-1", "websocket_url": "wss://x"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	_, _ = websh.CreateWebshSession(ac, "server-x", "", "", false, false, "")
	assert.False(t, hadKey)
}
```

Note: `CreateWebshSession` calls `term.GetSize` which may fail in test env. If so, the function returns early with an error before SendPostRequest — adjust by guarding or by accepting that the test asserts the field via a different path. If `term.GetSize` errors are blocking, refactor the body construction into a helper and unit-test the helper directly. Prefer the helper refactor for testability.

If the refactor is chosen: extract a `buildSessionRequest(serverID, username, groupname, rows, cols int, workSessionID string) *SessionRequest` function and test it in isolation:

```go
func TestBuildSessionRequest_OmitsEmptyWorkSession(t *testing.T) {
	req := websh.BuildSessionRequest("srv-1", "", "", 24, 80, "")
	assert.Empty(t, req.WorkSession)
}

func TestBuildSessionRequest_IncludesWorkSession(t *testing.T) {
	req := websh.BuildSessionRequest("srv-1", "", "", 24, 80, "ses-abc")
	assert.Equal(t, "ses-abc", req.WorkSession)
}
```

(Pick the refactor route — it's cleaner and avoids httptest+terminal weirdness.)

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./api/websh/ -v`
Expected: FAIL.

- [ ] **Step 3: Add field to `SessionRequest`**

In `api/websh/types.go`:

```go
type SessionRequest struct {
	Rows        int    `json:"rows"`
	Cols        int    `json:"cols"`
	Server      string `json:"server"`
	Username    string `json:"username"`
	Groupname   string `json:"groupname"`
	WorkSession string `json:"work_session,omitempty"`
}
```

- [ ] **Step 4: Extract helper + update signature**

In `api/websh/websh.go`:

```go
// BuildSessionRequest assembles the JSON body for a websh session create call.
// Empty workSessionID is omitted from the request (omitempty on the field).
func BuildSessionRequest(serverID, username, groupname string, rows, cols int, workSessionID string) *SessionRequest {
	return &SessionRequest{
		Server:      serverID,
		Username:    username,
		Groupname:   groupname,
		Rows:        rows,
		Cols:        cols,
		WorkSession: workSessionID,
	}
}

func CreateWebshSession(ac *client.AlpaconClient, serverName, username, groupname string, share, readOnly bool, workSessionID string) (SessionResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return SessionResponse{}, err
	}

	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return SessionResponse{}, err
	}

	sessionRequest := BuildSessionRequest(serverID, username, groupname, height, width, workSessionID)
	// ... rest unchanged ...
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./api/websh/ -v`
Expected: PASS.

- [ ] **Step 6: Don't commit yet** — `cmd/websh` callers need to be updated next.

---

## Task 10: cmd/websh wiring

**Files:**
- Modify: `cmd/websh/websh.go`
- Modify: `cmd/websh/websh_test.go`

`websh` uses `DisableFlagParsing: true` and has its own loop in `websh.go`. Both branches (command-mode → `RunCommandWithRetry`, session-mode → `CreateWebshSession`) need the UUID.

- [ ] **Step 1: Add failing test for flag parsing**

If `cmd/websh/websh_test.go` has unit tests for flag extraction, extend them. Otherwise, write tests against an extractable helper. Since the parsing loop is inline in `Run`, the practical approach is a small integration check via `cobra.Command.Execute()` with stubbed downstream. To keep this tractable, extract the loop into a function:

```go
// In cmd/websh/websh.go
type webshArgs struct {
	username, groupname, serverName string
	commandArgs                     []string
	share, readOnly                 bool
	workSessionID                   string
	env                             map[string]string
}

func parseWebshArgs(args []string) (webshArgs, error) { /* extracted loop */ }
```

Then test:

```go
func TestParseWebshArgs_WorkSessionFlag(t *testing.T) {
	got, err := websh.ParseWebshArgs([]string{"--work-session", "ses-abc", "my-server"})
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
	assert.Equal(t, "my-server", got.ServerName)
}

func TestParseWebshArgs_WorkSessionEqualForm(t *testing.T) {
	got, err := websh.ParseWebshArgs([]string{"--work-session=ses-abc", "my-server"})
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
}

func TestParseWebshArgs_CommandAfterServerNotConsumed(t *testing.T) {
	got, err := websh.ParseWebshArgs([]string{"my-server", "ls", "--work-session", "fake"})
	require.NoError(t, err)
	assert.Equal(t, "my-server", got.ServerName)
	assert.Equal(t, "", got.WorkSessionID)
	assert.Equal(t, []string{"ls", "--work-session", "fake"}, got.CommandArgs)
}
```

(Use whatever capitalization matches the exported helper; if the helper is internal, use `parseWebshArgs` in same-package test files. Recommend exporting since `cmd/websh` is the package, internal test is fine.)

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./cmd/websh/ -v`
Expected: FAIL.

- [ ] **Step 3: Extract `parseWebshArgs` and add `--work-session` case**

In `cmd/websh/websh.go`, factor the existing `Run` loop into a helper:

```go
type webshArgs struct {
	Username      string
	Groupname     string
	ServerName    string
	CommandArgs   []string
	Share         bool
	ReadOnly      bool
	WorkSessionID string
	Env           map[string]string
}

func parseWebshArgs(args []string) (webshArgs, error) {
	res := webshArgs{Env: map[string]string{}}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-s" || args[i] == "--share":
			res.Share = true
		case args[i] == "-h" || args[i] == "--help":
			return res, errHelpRequested
		case strings.HasPrefix(args[i], "-u") || strings.HasPrefix(args[i], "--username"):
			res.Username, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "-g") || strings.HasPrefix(args[i], "--groupname"):
			res.Groupname, i = extractValue(args, i)
		case strings.HasPrefix(args[i], "--env"):
			i = extractEnvValue(args, i, res.Env)
		case strings.HasPrefix(args[i], "--read-only"):
			// ... existing logic, set res.ReadOnly ...
		case args[i] == "--work-session" || strings.HasPrefix(args[i], "--work-session="):
			res.WorkSessionID, i = extractValue(args, i)
		default:
			if res.ServerName == "" {
				res.ServerName = args[i]
			} else {
				res.CommandArgs = append(res.CommandArgs, args[i:]...)
				i = len(args)
			}
		}
	}
	return res, nil
}

var errHelpRequested = errors.New("help requested")
```

(Adjust to match existing `extractValue` signature exactly. The pre-existing `--read-only` block must be preserved verbatim — copy into the helper.)

In `WebshCmd.Run`, replace the inline loop with `parsed, err := parseWebshArgs(args)` and handle the help sentinel error.

- [ ] **Step 4: Wire UUID resolution into both branches**

After `parsed` is built and before either branch executes, add:

```go
uuid, err := worksession.Resolve(parsed.WorkSessionID)
if err != nil {
	utils.CliErrorWithExit("%s", err)
}
worksession.AnnounceIfActive(uuid)
```

Then:

- Command-mode branch: `result, err := execCmd.RunCommandWithRetry(alpaconClient, serverName, command, username, groupname, env, uuid)`
- Session-mode branch: `session, err := websh.CreateWebshSession(alpaconClient, serverName, username, groupname, share, readOnly, uuid)` (also update the retry callback)

Import: `"github.com/alpacax/alpacon-cli/cmd/worksession"`.

Update the `Long`/`Example` doc strings to mention `--work-session`.

- [ ] **Step 5: Run tests + build**

```bash
go test ./cmd/websh/ ./cmd/exec/ ./api/websh/ -v
go build ./...
```

Expected: PASS, build clean.

- [ ] **Step 6: Commit**

```bash
git add api/websh/types.go api/websh/websh.go api/websh/websh_test.go cmd/websh/websh.go cmd/websh/websh_test.go
git commit -m "feat(websh): add --work-session flag with config-tier fallback"
```

---

## Task 11: API tunnel.CreateTunnelSession + pkg/tunnel/runtime + cmd/tunnel

**Files:**
- Modify: `api/tunnel/types.go`
- Modify: `api/tunnel/tunnel.go`
- Create or modify: `api/tunnel/tunnel_test.go`
- Modify: `pkg/tunnel/runtime/runtime.go`
- Modify: `cmd/tunnel/tunnel.go`

- [ ] **Step 1: Write failing test for body field**

Create `api/tunnel/tunnel_test.go`:

```go
package tunnel_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTunnelSession_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var sawValue string
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/api/servers/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
			return
		}
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/api/websh/tunnels/") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			v, ok := payload["work_session"]
			hadKey = ok
			if ok {
				sawValue, _ = v.(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "t-1", "websocket_url": "wss://x"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	_, _ = tunnel.CreateTunnelSession(ac, "server-x", "", "", 22, "ses-abc")
	require.True(t, hadKey)
	assert.Equal(t, "ses-abc", sawValue)
}

func TestCreateTunnelSession_BodyOmitsWorkSession_WhenEmpty(t *testing.T) {
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		_, hadKey = payload["work_session"]
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t-1"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	_, _ = tunnel.CreateTunnelSession(ac, "server-x", "", "", 22, "")
	assert.False(t, hadKey)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./api/tunnel/ -v`
Expected: FAIL.

- [ ] **Step 3: Add field, update signature**

`api/tunnel/types.go`:

```go
type TunnelSessionRequest struct {
	Server      string `json:"server"`
	TargetPort  int    `json:"target_port"`
	Username    string `json:"username"`
	Groupname   string `json:"groupname"`
	ClientType  string `json:"client_type"`
	WorkSession string `json:"work_session,omitempty"`
}
```

(Match the field order/tags exactly to the existing struct; the existing fields may differ slightly. Read the file before editing.)

`api/tunnel/tunnel.go`:

```go
func CreateTunnelSession(ac *client.AlpaconClient, serverName, username, groupname string, targetPort int, workSessionID string) (*TunnelSessionResponse, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to get server ID: %w", err)
	}

	request := TunnelSessionRequest{
		Server:      serverID,
		TargetPort:  targetPort,
		Username:    username,
		Groupname:   groupname,
		ClientType:  "cli",
		WorkSession: workSessionID,
	}
	// ... rest unchanged ...
}
```

- [ ] **Step 4: Update `StartOptions` in `pkg/tunnel/runtime/runtime.go`**

Read the file to confirm structure. Add field:

```go
type StartOptions struct {
	ServerName    string
	LocalPort     string
	RemotePort    string
	Username      string
	Groupname     string
	Verbose       bool
	WorkSessionID string
}
```

In `Start`, update the call:

```go
tunnelSession, err := tunnelapi.CreateTunnelSession(alpaconClient, opts.ServerName, opts.Username, opts.Groupname, targetPort, opts.WorkSessionID)
```

- [ ] **Step 5: Wire `cmd/tunnel/tunnel.go`**

Add to `tunnelFlagValues`:

```go
type tunnelFlagValues struct {
	localPort     string
	remotePort    string
	username      string
	groupname     string
	verbose       bool
	workSessionID string
}
```

In `bindTunnelFlags`:

```go
cmd.Flags().StringVar(&flags.workSessionID, "work-session", "", "Attach this tunnel to a work-session (overrides 'work-session use').")
```

In `toStartOptions`:

```go
func (f tunnelFlagValues) toStartOptions(serverName string) tunnelruntime.StartOptions {
	uuid, err := worksession.Resolve(f.workSessionID)
	if err != nil {
		utils.CliErrorWithExit("%s", err)
	}
	worksession.AnnounceIfActive(uuid)
	return tunnelruntime.StartOptions{
		ServerName:    serverName,
		LocalPort:     f.localPort,
		RemotePort:    f.remotePort,
		Username:      f.username,
		Groupname:     f.groupname,
		Verbose:       f.verbose,
		WorkSessionID: uuid,
	}
}
```

Add import: `"github.com/alpacax/alpacon-cli/cmd/worksession"`.

- [ ] **Step 6: Run tests + build**

```bash
go test ./api/tunnel/ ./pkg/tunnel/... ./cmd/tunnel/ -v
go build ./...
```

Expected: PASS, build clean.

- [ ] **Step 7: Commit**

```bash
git add api/tunnel/types.go api/tunnel/tunnel.go api/tunnel/tunnel_test.go pkg/tunnel/runtime/runtime.go cmd/tunnel/tunnel.go
git commit -m "feat(tunnel): add --work-session flag with config-tier fallback"
```

---

## Task 12: API ftp + cmd/ftp/cp wiring

**Files:**
- Modify: `api/ftp/ftp.go`
- Create or modify: `api/ftp/ftp_test.go`
- Modify: `cmd/ftp/cp.go`

The `ftp` server endpoint does not yet accept `work_session` (cross-repo dependency). CLI sends the field regardless — the field will either be ignored by older server versions or accepted by the new version when it lands.

- [ ] **Step 1: Read `api/ftp/ftp.go` to identify the JSON body construction sites**

Run: `grep -nE "SendPostRequest|json\.Marshal|Upload(File|Folder)|DownloadFile|type .*Request" api/ftp/ftp.go | head -40`

Note the request struct(s) used for upload/download. Likely one or more `*Request` types — find the one that becomes the JSON body sent to the FTP create endpoint.

- [ ] **Step 2: Write failing test (one per direction)**

Append to `api/ftp/ftp_test.go`:

```go
func TestUploadFile_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var sawValue string
	var hadKey bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			v, ok := payload["work_session"]
			hadKey = ok
			if ok {
				sawValue, _ = v.(string)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ft-1"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: ts.URL}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "f.txt")
	require.NoError(t, os.WriteFile(src, []byte("hi"), 0o644))

	_ = ftp.UploadFile(ac, []string{src}, "my-server:/tmp/", "", "", true, "ses-abc")

	require.True(t, hadKey)
	assert.Equal(t, "ses-abc", sawValue)
}
```

(Mirror the test for `DownloadFile` and `UploadFolder` — same body assertion pattern.)

Test caveat: real `UploadFile` may do server ID lookup and multipart upload — extend the handler to satisfy whichever upstream calls happen first. If the body-assertion path is unreachable in a httptest, factor the body construction into a small testable function (e.g., `buildFTPCreateRequest`) and unit-test that function directly — same pattern as Task 9.

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./api/ftp/ -v`
Expected: FAIL.

- [ ] **Step 4: Add field and parameter**

In `api/ftp/ftp.go`, add to the relevant request struct(s):

```go
WorkSession string `json:"work_session,omitempty"`
```

Update signatures:

```go
func UploadFile(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool, workSessionID string) error
func UploadFolder(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool, workSessionID string) error
func DownloadFile(ac *client.AlpaconClient, sources []string, dest, username, groupname string, recursive bool, workSessionID string) error
```

In each function body, where the request struct is built, set `WorkSession: workSessionID`.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./api/ftp/ -v`
Expected: PASS.

- [ ] **Step 6: Wire `cmd/ftp/cp.go`**

Add flag declaration in `init()`:

```go
CpCmd.Flags().String("work-session", "", "Attach this transfer to a work-session (overrides 'work-session use').")
```

In `Run`, after reading other flags:

```go
flagWorkSession, _ := cmd.Flags().GetString("work-session")
uuid, err := worksession.Resolve(flagWorkSession)
if err != nil {
	utils.CliErrorWithExit("%s", err)
}
worksession.AnnounceIfActive(uuid)
```

Pass `uuid` to both branches:

```go
err := uploadObject(alpaconClient, sources, dest, username, groupname, recursive, allowOverwrite, uuid)
// ... and:
err := downloadObject(alpaconClient, sources, dest, username, groupname, recursive, uuid)
```

Update `uploadObject` and `downloadObject` signatures to accept and forward `workSessionID`:

```go
func uploadObject(client *client.AlpaconClient, src []string, dest, username, groupname string, recursive, allowOverwrite bool, workSessionID string) error {
	if recursive {
		err = ftp.UploadFolder(client, src, dest, username, groupname, allowOverwrite, workSessionID)
	} else {
		err = ftp.UploadFile(client, src, dest, username, groupname, allowOverwrite, workSessionID)
	}
	// ... unchanged ...
}

func downloadObject(client *client.AlpaconClient, sources []string, dest, username, groupname string, recursive bool, workSessionID string) error {
	err := ftp.DownloadFile(client, sources, dest, username, groupname, recursive, workSessionID)
	// ... unchanged ...
}
```

Also update the `RetryOperation` closures in `Run` to forward `uuid`.

Add import: `"github.com/alpacax/alpacon-cli/cmd/worksession"`. Update the `Long` doc string to mention the flag.

- [ ] **Step 7: Run tests + build**

```bash
go test ./api/ftp/ ./cmd/ftp/ -v
go build ./...
```

Expected: PASS, build clean.

- [ ] **Step 8: Commit**

```bash
git add api/ftp/ftp.go api/ftp/ftp_test.go cmd/ftp/cp.go
git commit -m "feat(cp): add --work-session flag (server-side ftp support pending)"
```

---

## Task 13: Final verification

- [ ] **Step 1: Run full test suite with race detection**

Run: `go test -race ./...`
Expected: all packages PASS.

- [ ] **Step 2: Lint**

Run: `golangci-lint run ./...`
Expected: no findings. If errcheck complains about close calls in new tests, apply the `defer func() { _ = x.Close() }()` pattern documented in `CLAUDE.md`.

- [ ] **Step 3: Smoke-test help output**

```bash
go build -o /tmp/alpacon . && \
/tmp/alpacon work-session --help && \
/tmp/alpacon work-session use --help && \
/tmp/alpacon work-session current --help && \
/tmp/alpacon exec --help && \
/tmp/alpacon websh --help && \
/tmp/alpacon cp --help && \
/tmp/alpacon tunnel --help
```

Verify `--work-session` shows up in `exec`/`websh`/`cp`/`tunnel` help, and `use`/`current` appear as subcommands of `work-session`.

- [ ] **Step 4: Final commit if any documentation tweaks were needed**

```bash
git status
# If anything is dirty (doc fixes, etc.), commit it:
# git commit -am "docs(worksession): help-text polish"
```

---

## Self-review checklist (run before handing off)

1. **Spec coverage** — Every section of [`2026-05-11-worksession-switching.md`](2026-05-11-worksession-switching.md) maps to at least one task:
   - §2.1 use/current → Tasks 4, 5
   - §2.2 ls marker → Task 6
   - §2.2 flags on exec/websh/cp/tunnel → Tasks 8, 10, 12, 11
   - §2.3 resolution order → Task 3
   - §2.4 execution notice → Task 3 (AnnounceIfActive used by all 4 commands)
   - §3.3 config schema → Tasks 1, 2
   - §3.5 API signatures → Tasks 7, 9, 11, 12
   - §4 error handling → Tasks 4, 5 (use/unset/current); 8/10/11/12 (server reject propagates verbatim by reusing existing error paths)
   - §5 testing → tests included in every task

2. **Placeholder scan** — No TBD/TODO/"handle edge cases" without code.

3. **Type consistency** — `workSessionID string` parameter name used uniformly across `RunCommand`, `CreateWebshSession`, `UploadFile`, `UploadFolder`, `DownloadFile`, `CreateTunnelSession`. Struct field `WorkSession string \`json:"work_session,omitempty"\`` uniform across `CommandRequest`, `SessionRequest`, `TunnelSessionRequest`, and the ftp request struct.

## Out of scope (not implemented in this plan)

- Server-side `ftp` serializer `work_session` field — alpacon-server PR
- `editor`/`sudo` CLI commands — they don't exist today
- `ALPACON_WORK_SESSION` env var — explicitly excluded per design
- `--no-work-session` one-time bypass — explicitly excluded per design
- Showing session name in execution notice — ID only (per design)
