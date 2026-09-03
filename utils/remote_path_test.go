package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteFileName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remotePath string
		want       string
		wantErr    bool
	}{
		{name: "absolute file", remotePath: "/etc/app.conf", want: "app.conf"},
		{name: "relative file", remotePath: "logs/app.log", want: "app.log"},
		{name: "empty path", remotePath: "", wantErr: true},
		{name: "current directory basename", remotePath: "/tmp/.", wantErr: true},
		{name: "parent directory basename", remotePath: "/tmp/..", wantErr: true},
		{name: "trailing slash", remotePath: "/tmp/app.conf/", wantErr: true},
		{name: "backslash basename", remotePath: `/tmp/..\saved`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RemoteFileName(tt.remotePath)
			if tt.wantErr {
				require.ErrorContains(t, err, "file name")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
