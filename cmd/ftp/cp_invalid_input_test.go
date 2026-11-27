package ftp

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	refusalDownloadRoute     = "POST /api/webftp/downloads/"
	refusalBulkDownloadRoute = "POST /api/webftp/downloads/bulk/"

	accountRefusal     = "the server account or group is not valid on this server, or you may not use it—name one with -u USERNAME or -g GROUPNAME where the command takes them, or use a work session that names an account"
	nameTooLongRefusal = "the file or folder name is too long—a file name may be up to 128 characters, and a folder downloaded as an archive up to 124; rename it and try again"
	twoFoldersRefusal  = "the files are in different folders—transfer files from one folder at a time"
)

var cpFormatRunners = map[string]func(cmdtest.Build, ...string) (string, string, int){
	utils.OutputFormatTable: cmdtest.RunCaptured,
	utils.OutputFormatJSON:  cmdtest.RunJSON,
}

// Serial: cpScene sets HOME.
func TestCpUploadOfARejectedAccountSaysToNameAnother(t *testing.T) {
	for format, runCp := range cpFormatRunners {
		t.Run(format, func(t *testing.T) {
			ts, source := cpScene(t, http.StatusBadRequest, genericRefusalBody("non_field_errors", "server_username_groupname_invalid"))

			_, stderr, code := runCp(func(ios *utils.IOStreams) *cobra.Command { return NewCpCmd(ts.Factory(), ios) }, source, "my-server:/tmp/")

			assert.Equal(t, utils.ExitCodeGeneralError, code, stderr)
			assert.Equal(t, 1, ts.Hits(uploadCreateRoute))
			assertRuleNamed(t, stderr, format, "server_username_groupname_invalid", "non_field_errors", "Failed to upload to 'my-server:/tmp/': "+accountRefusal)
		})
	}
}

// Serial: it sets HOME.
func TestCpDownloadNamesTheRuleBehindAnInvalidInputRefusal(t *testing.T) {
	longFolder := "my-server:/data/" + strings.Repeat("a", 126)
	tests := []struct {
		name  string
		route string
		field string
		code  string
		args  []string
		want  string
	}{
		{
			name:  "rejected account",
			route: refusalDownloadRoute,
			field: "non_field_errors",
			code:  "server_username_groupname_invalid",
			args:  []string{"my-server:/etc/app.conf"},
			want:  "Failed to download from '[my-server:/etc/app.conf]': " + accountRefusal,
		},
		{
			name:  "folder name too long",
			route: refusalDownloadRoute,
			field: "non_field_errors",
			code:  "webftp_name_too_long",
			args:  []string{"-r", longFolder},
			want:  "Failed to download from '[" + longFolder + "]': " + nameTooLongRefusal,
		},
		{
			name:  "files from two folders",
			route: refusalBulkDownloadRoute,
			field: "path",
			code:  "webftp_invalid_path",
			args:  []string{"my-server:/etc/app.conf", "my-server:/var/log/app.log"},
			want:  "Failed to download from '[my-server:/etc/app.conf, my-server:/var/log/app.log]': " + twoFoldersRefusal,
		},
	}
	for _, tt := range tests {
		for format, runCp := range cpFormatRunners {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Setenv("ALPACON_WORK_SESSION", "")
				dest := filepath.Join(home, "out")
				require.NoError(t, os.Mkdir(dest, 0o700))
				ts := cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/servers/servers/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47", "name": "my-server"}]}`},
					tt.route:                    {Status: http.StatusBadRequest, Body: genericRefusalBody(tt.field, tt.code)},
				})

				_, stderr, code := runCp(func(ios *utils.IOStreams) *cobra.Command { return NewCpCmd(ts.Factory(), ios) }, slices.Concat(tt.args, []string{dest})...)

				assert.Equal(t, utils.ExitCodeGeneralError, code, stderr)
				assert.Equal(t, 1, ts.Hits(tt.route))
				assertRuleNamed(t, stderr, format, tt.code, tt.field, tt.want)
			})
		}
	}
}

// genericRefusalBody is a 400 body naming code under field with only the generic message.
func genericRefusalBody(field, code string) string {
	return `{"code":"` + code + `","field_errors":{"` + field + `":[{"code":"` + code + `","message":"Invalid input."}]}}`
}

// assertRuleNamed checks the whole failure output, and under JSON that the envelope keeps the status and the server's field errors.
func assertRuleNamed(t *testing.T, stderr, format, wantCode, wantField, wantMessage string) {
	t.Helper()
	if format == utils.OutputFormatTable {
		assert.Equal(t, "Error: "+wantMessage+"\n", stderr)
		return
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	var envelope utils.JSONErrorEnvelope[map[string]any]
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &envelope), stderr)
	assert.False(t, envelope.OK)
	assert.Equal(t, utils.ExitCodeGeneralError, envelope.ExitCode)
	assert.Equal(t, wantCode, envelope.ErrorCode)
	assert.Equal(t, wantMessage, envelope.Message)
	assert.Equal(t, http.StatusBadRequest, envelope.HTTPStatus)
	assert.Equal(t, map[string]any{wantField: []any{map[string]any{"code": wantCode, "message": "Invalid input."}}}, envelope.Server["field_errors"])
}
