package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoteFileName(t *testing.T) {
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
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "file name")
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
