package approval

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
