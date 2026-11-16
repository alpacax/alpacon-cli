package approval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filterCaseName names the empty filter "empty": t.Run("") would name it #00, and the empty
// filter is the case that matters most, since it is what an unset flag sends.
func filterCaseName(input string) string {
	if input == "" {
		return "empty"
	}
	return input
}

func TestValidateStatusFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"", false},
		{"pending", false},
		{"approved", false},
		{"rejected", false},
		{"cancelled", false},
		{"expired", false},
		{"recorded", false},
		{"unknown", true},
		{"PENDING", true},
		{"active", true},
	}
	for _, tc := range cases {
		t.Run(filterCaseName(tc.input), func(t *testing.T) {
			err := validateStatusFilter(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateTypeFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"", false},
		{"sudo", false},
		{"work_session", false},
		{"username", false},
		{"groupname", false},
		{"service_token", false},
		{"svc_token_mod", false},
		{"app_username", false},
		{"work_session_mod", false},
		{"sudo_policy", false},
		{"command_exec", false},
		{"discovered_account_accept", false},
		{"api_token_scope_expand", false},
		{"bad_type", true},
		{"WorkSession", true},
	}
	for _, tc := range cases {
		t.Run(filterCaseName(tc.input), func(t *testing.T) {
			err := validateTypeFilter(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestListRequestsEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		my       bool
		wantPath string
	}{
		{"default hits the approver queue", false, "/api/approvals/approvals/"},
		{"my hits my-requests", true, "/api/approvals/approvals/-/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotPaths []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPaths = append(gotPaths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[approvalapi.ApprovalRequest]{
					Count:   1,
					Results: []approvalapi.ApprovalRequest{{ID: "apr-1", RequestType: "sudo", Status: "pending"}},
				})
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			list, err := listRequests(ac, tc.my, "pending", "")

			require.NoError(t, err)
			assert.Equal(t, []string{tc.wantPath}, gotPaths)
			require.Len(t, list, 1)
			assert.Equal(t, "apr-1", list[0].ID)
		})
	}
}

// Serial: the command's flags write package-level variables, and the success
// case captures stdout and sets utils.OutputFormat.
func TestApprovalListCommand(t *testing.T) {
	const listBody = `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "request_type": "sudo", "request_data": "systemctl restart nginx", "status": "pending", "requested_by": {"name": "alice"}, "added_at": "2026-11-01T09:00:00Z"}]}`
	tests := []struct {
		name      string
		args      []string
		wantRoute string
	}{
		{name: "requests you can decide", args: nil, wantRoute: "GET /api/approvals/approvals/"},
		{name: "requests you submitted", args: []string{"--my"}, wantRoute: "GET /api/approvals/approvals/-/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/approvals/approvals/":   {Status: http.StatusOK, Body: listBody},
				"GET /api/approvals/approvals/-/": {Status: http.StatusOK, Body: listBody},
			})

			stdout, stderr, code := cmdtest.RunJSON(t, newApprovalListCmd(ts.Factory()), tt.args...)

			assert.Equal(t, 0, code, stderr)
			assert.Equal(t, 1, ts.Hits(tt.wantRoute))
			assert.Contains(t, stdout, `"550e8400-e29b-41d4-a716-446655440000"`)
			assert.Contains(t, stdout, `"systemctl restart nginx"`)
		})
	}
}

// Serial: the command's flags write package-level variables.
func TestApprovalListCommandFails(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		status   int
		wantCode int
		want     string
		wantHits int
	}{
		{name: "unknown status", args: []string{"--status", "open"}, wantCode: utils.ExitCodeUsageError, want: "invalid --status \"open\""},
		{name: "server error", status: http.StatusInternalServerError, wantCode: utils.ExitCodeGeneralError, want: "Failed to list approval requests", wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/approvals/approvals/": {Status: tt.status, Body: `{"detail": "unavailable"}`},
			})

			failure, code := cmdtest.Run(newApprovalListCmd(ts.Factory()), tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, failure, tt.want)
			assert.Equal(t, tt.wantHits, ts.Hits("GET /api/approvals/approvals/"))
		})
	}
}
