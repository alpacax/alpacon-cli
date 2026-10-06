package approval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filterCaseName keeps the empty filter identifiable. t.Run("") names the subtest
// #00, which says nothing about the row that failed, and the case that matters
// most here is exactly the empty one: it is what an unset flag sends.
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
