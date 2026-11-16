package utils

import (
	"runtime"
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

func TestRemotePathBase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remotePath string
		windows    bool
		want       string
		unixOnly   bool // a Windows client splits a backslash and a drive in any name
	}{
		{name: "linux path", remotePath: "/home/alice/report.txt", want: "report.txt"},
		{name: "linux trailing slash", remotePath: "/home/alice/logs/", want: "logs"},
		{name: "linux file name holding a backslash", remotePath: `/home/alice/a\b.txt`, want: `a\b.txt`, unixOnly: true},
		{name: "linux file name with a drive-like colon", remotePath: "a:notes.txt", want: "a:notes.txt", unixOnly: true},
		{name: "windows drive backslash path", remotePath: `C:\Users\alice\report.txt`, windows: true, want: "report.txt"},
		{name: "windows drive forward slash path", remotePath: "C:/Users/alice/report.txt", windows: true, want: "report.txt"},
		{name: "windows drive mixed separators", remotePath: `C:/Users\alice/report.txt`, windows: true, want: "report.txt"},
		{name: "windows drive trailing backslash", remotePath: `C:\Users\alice\logs\`, windows: true, want: "logs"},
		{name: "windows drive relative path", remotePath: "C:report.txt", windows: true, want: "report.txt"},
		{name: "windows drive root", remotePath: `C:\`, windows: true, want: "/"},
		{name: "windows rooted path without a drive", remotePath: `\Users\alice\report.txt`, windows: true, want: "report.txt"},
		{name: "windows relative path", remotePath: `Documents\report.txt`, windows: true, want: "report.txt"},
		{name: "windows unc path", remotePath: `\\fileserver\share\docs\report.txt`, windows: true, want: "report.txt"},
		{name: "windows unc share root", remotePath: `\\fileserver\share`, windows: true, want: "."},
		{name: "windows unc forward slash after share", remotePath: `\\fileserver\share/docs/report.txt`, windows: true, want: "report.txt"},
		{name: "windows unc forward slashes", remotePath: "//fileserver/share/report.txt", windows: true, want: "report.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.unixOnly && runtime.GOOS == "windows" {
				t.Skip("a Windows client cannot keep a backslash or a drive in a file name")
			}

			got := RemotePathBase(tt.remotePath, tt.windows)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRemotePathDir(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remotePath string
		windows    bool
		want       string
	}{
		{name: "linux path", remotePath: "/home/alice/report.txt", want: "/home/alice"},
		{name: "linux bare name", remotePath: "report.txt", want: "."},
		{name: "linux name holding backslashes", remotePath: `C:\Users\alice\report.txt`, want: "."},
		{name: "windows drive backslash path", remotePath: `C:\Users\alice\report.txt`, windows: true, want: `C:\Users\alice`},
		{name: "windows drive forward slash path", remotePath: "C:/Users/alice/report.txt", windows: true, want: `C:\Users\alice`},
		{name: "windows drive root file", remotePath: `C:\report.txt`, windows: true, want: `C:\`},
		{name: "windows rooted path without a drive", remotePath: `\Users\alice\report.txt`, windows: true, want: `\Users\alice`},
		{name: "windows relative path", remotePath: `Documents\report.txt`, windows: true, want: "Documents"},
		{name: "windows unc path", remotePath: `\\fileserver\share\docs\report.txt`, windows: true, want: `\\fileserver\share\docs`},
		{name: "windows unc forward slash after share", remotePath: `\\fileserver\share/docs/report.txt`, windows: true, want: `\\fileserver\share\docs`},
		{name: "windows unc share root", remotePath: `\\fileserver\share`, windows: true, want: `\\fileserver\share\`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := RemotePathDir(tt.remotePath, tt.windows)

			assert.Equal(t, tt.want, got)
		})
	}
}
