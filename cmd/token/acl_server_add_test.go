package token

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateServerAclFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		serverName string
		serversCSV string
		wantErr    string
	}{
		{
			name:       "neither flag set",
			serverName: "",
			serversCSV: "",
			wantErr:    "one of --server or --servers is required",
		},
		{
			name:       "both flags set",
			serverName: "my-server",
			serversCSV: "web-01",
			wantErr:    "use either --server or --servers, not both",
		},
		{
			name:       "only server set",
			serverName: "my-server",
			serversCSV: "",
			wantErr:    "",
		},
		{
			name:       "only servers set",
			serverName: "",
			serversCSV: "web-01,web-02",
			wantErr:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateServerAclFlags(tc.serverName, tc.serversCSV)

			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.wantErr)
		})
	}
}
