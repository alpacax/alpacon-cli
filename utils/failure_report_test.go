package utils

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailureEnvelopeCarriesRemoteExitCodeAndPartial(t *testing.T) {
	t.Parallel()
	remote := 9

	env := Failure{
		ExitCode: 9, ErrorCode: ErrorCodeRemoteCommandFailed, Message: "m", Operation: "exec",
		RemoteExitCode: &remote, Partial: &PartialInfo{Count: 2},
	}.envelope()

	assert.Equal(t, 9, env.ExitCode)
	assert.Equal(t, ErrorCodeRemoteCommandFailed, env.ErrorCode)
	assert.Equal(t, "exec", env.Context.Operation)
	require.NotNil(t, env.RemoteExitCode)
	assert.Equal(t, 9, *env.RemoteExitCode)
	assert.Equal(t, 2, env.Partial.Count)
}

func TestFailureEnvelopeClassifiesAnEmptyCodeFromErr(t *testing.T) {
	t.Parallel()
	env := Failure{ExitCode: 1, Err: &fakeAPIError{code: "rbac_permission_required", status: 403}, Message: "m"}.envelope()

	assert.Equal(t, "rbac_permission_required", env.ErrorCode)
	assert.Equal(t, 403, env.HTTPStatus)
}

// Serial: the child reaches an os.Exit.
func TestGuardsEndWithTheirCodeUnderJSON(t *testing.T) {
	tests := []struct {
		mode     string
		format   string
		wantCode string
	}{
		{"confirm", OutputFormatJSON, ErrorCodeConfirmationRequired},
		{"confirm", OutputFormatTable, ""},
		{"exit-failure", OutputFormatJSON, ErrorCodeNotApproved},
		{"exit-failure", OutputFormatTable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.mode+"_"+tt.format, func(t *testing.T) {
			helper := exec.Command(os.Args[0], "-test.run=^TestGuardsHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_GUARDS_HELPER="+tt.mode+":"+tt.format)
			var stderr bytes.Buffer
			helper.Stderr = &stderr

			err := helper.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			if tt.wantCode == "" {
				assert.NotContains(t, stderr.String(), "error_code")
				return
			}
			env := decodeSingleLineEnvelope(t, stderr.String())
			assert.Equal(t, exitErr.ExitCode(), env.ExitCode)
			assert.Equal(t, tt.wantCode, env.ErrorCode)
		})
	}
}

func TestGuardsHelperProcess(t *testing.T) {
	spec := os.Getenv("GO_WANT_GUARDS_HELPER")
	if spec == "" {
		return
	}
	mode, format, _ := strings.Cut(spec, ":")
	OutputFormat = format
	switch mode {
	case "confirm":
		ConfirmAction("delete it?")
	case "exit-failure":
		ExitWithFailureJSON(Failure{ExitCode: ExitCodeNotApproved, ErrorCode: ErrorCodeNotApproved, Message: "no"})
		os.Exit(ExitCodeNotApproved)
	}
	os.Exit(0)
}
