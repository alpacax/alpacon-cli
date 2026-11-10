package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkSessionWarningInvocation(t *testing.T) {
	const warning = "Warning: starting December 1, 2026, this personal API token will need a Work Session for this action. Open a Work Session, or use a service token for automation.\n"
	for _, tt := range []struct {
		name   string
		header string
		sunset string
		mode   string
		want   string
	}{
		{name: "standard", header: "work_session_required_soon", sunset: "Tue, 01 Dec 2026 00:00:00 GMT", mode: "standard", want: warning},
		{name: "raw", header: "work_session_required_soon", sunset: "Tue, 01 Dec 2026 00:00:00 GMT", mode: "raw", want: warning},
		{name: "download", header: "work_session_required_soon", sunset: "Tue, 01 Dec 2026 00:00:00 GMT", mode: "download", want: warning},
		{name: "multiple clients and response paths", header: "work_session_required_soon", sunset: "Tue, 01 Dec 2026 00:00:00 GMT", mode: "mixed", want: warning},
		{name: "absent", sunset: "Tue, 01 Dec 2026 00:00:00 GMT", mode: "standard"},
		{name: "unknown", header: "future_warning", sunset: "Tue, 01 Dec 2026 00:00:00 GMT", mode: "standard"},
		{name: "invalid sunset", header: "work_session_required_soon", sunset: "not a date", mode: "standard"},
		{name: "missing sunset", header: "work_session_required_soon", mode: "standard"},
		{name: "date from server", header: "work_session_required_soon", sunset: "Fri, 15 Jan 2027 00:00:00 GMT", mode: "standard", want: "Warning: starting January 15, 2027, this personal API token will need a Work Session for this action. Open a Work Session, or use a service token for automation.\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestWorkSessionWarningHelper$")
			cmd.Env = append(os.Environ(), "ALPACON_WARNING_TEST=1", "ALPACON_WARNING_VALUE="+tt.header, "ALPACON_WARNING_SUNSET="+tt.sunset, "ALPACON_WARNING_MODE="+tt.mode)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			require.NoError(t, cmd.Run())
			assert.Equal(t, 0, cmd.ProcessState.ExitCode())
			assert.Equal(t, "{\"ok\":true}\n{\"ok\":true}\n", stdout.String())
			assert.Equal(t, tt.want, stderr.String())
		})
	}
}

func TestWorkSessionWarningHelper(t *testing.T) {
	if os.Getenv("ALPACON_WARNING_TEST") != "1" {
		return
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Alpacon-Warning", os.Getenv("ALPACON_WARNING_VALUE"))
		w.Header().Set("Sunset", os.Getenv("ALPACON_WARNING_SUNSET"))
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	// Separate clients and repeated requests must share the same warning budget.
	for i := range 2 {
		ac := newTestClient(ts.URL)
		mode := os.Getenv("ALPACON_WARNING_MODE")
		if mode == "mixed" {
			mode = []string{"standard", "raw"}[i]
		}
		var body []byte
		var err error
		switch mode {
		case "raw":
			var response *RawResponse
			response, err = ac.SendRawRequest(http.MethodGet, "/api/test/", nil, nil)
			if err == nil {
				body = response.Body
			}
		case "download":
			var response *http.Response
			response, err = ac.SendGetRequestForDownload("/api/test/")
			if err == nil {
				body, err = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
		default:
			body, err = ac.SendGetRequest("/api/test/")
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintln(os.Stdout, string(body))
	}
	ts.Close()
	os.Exit(0)
}

func TestWorkSessionRequiredShowsServerDetailAndHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"work_session_required","detail":"Open a Work Session for this action.","hint":"Use a service token for automation."}`)
	}))
	defer ts.Close()
	_, err := newTestClient(ts.URL).SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "work_session_required", utils.StructuredErrorCode(err))
	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err))
	stdout, stderr := testutil.CaptureOutput(t, func() { utils.CliError("%s", err) })
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Open a Work Session for this action.")
	assert.Contains(t, stderr, "Hint: Use a service token for automation.")
}
