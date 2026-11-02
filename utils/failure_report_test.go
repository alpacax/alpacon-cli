package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailureEnvelopeCarriesRemoteExitCodeAndPartial(t *testing.T) {
	t.Parallel()
	remote := 9

	env := Failure{
		ExitCode: 9, ErrorCode: ErrorCodeCLI, Message: "m", Operation: "exec",
		RemoteExitCode: &remote, Partial: &PartialInfo{Count: 2},
	}.envelope()

	assert.Equal(t, 9, env.ExitCode)
	assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
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
