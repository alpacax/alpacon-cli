package token

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateServerAclFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		serverName     string
		serversCSV     string
		wantErr        string
		wantServerName string
		wantServersCSV string
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
			name:           "only server set",
			serverName:     "my-server",
			serversCSV:     "",
			wantErr:        "",
			wantServerName: "my-server",
		},
		{
			name:           "only servers set",
			serverName:     "",
			serversCSV:     "web-01,web-02",
			wantErr:        "",
			wantServersCSV: "web-01,web-02",
		},
		{
			name:       "whitespace only server set",
			serverName: "   ",
			serversCSV: "",
			wantErr:    "one of --server or --servers is required",
		},
		{
			name:       "whitespace only servers set",
			serverName: "",
			serversCSV: "   ",
			wantErr:    "one of --server or --servers is required",
		},
		{
			name:           "server with surrounding whitespace",
			serverName:     "  web-01  ",
			serversCSV:     "",
			wantErr:        "",
			wantServerName: "web-01",
		},
		{
			name:           "whitespace only server with servers set",
			serverName:     "   ",
			serversCSV:     "web-01",
			wantErr:        "",
			wantServersCSV: "web-01",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotServerName, gotServersCSV, err := validateServerAclFlags(tc.serverName, tc.serversCSV)

			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.wantServerName, gotServerName)
				assert.Equal(t, tc.wantServersCSV, gotServersCSV)
				return
			}
			assert.EqualError(t, err, tc.wantErr)
		})
	}
}
