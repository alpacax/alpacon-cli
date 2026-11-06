package iam

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsernameErrorMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code      string
		wantOK    bool
		wantMatch string
	}{
		{"user_username_invalid", true, "lowercase letters"},
		{"user_username_disallowed", true, "reserved"},
		{"user_username_in_use", true, "already in use"},
		{"user_username_already_set", true, "already set"},
		{"approval_superuser_approve_required", true, "superuser approval"},
		{"some_unknown_code", false, ""},
		{"", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			msg, ok := UsernameErrorMessage(tt.code)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Contains(t, msg, tt.wantMatch)
			} else {
				assert.Empty(t, msg)
			}
		})
	}
}

func TestIsRetryableUsernameError(t *testing.T) {
	t.Parallel()
	assert.True(t, isRetryableUsernameError("user_username_invalid"))
	assert.True(t, isRetryableUsernameError("user_username_disallowed"))
	assert.True(t, isRetryableUsernameError("user_username_in_use"))
	assert.False(t, isRetryableUsernameError("user_username_already_set"))
	assert.False(t, isRetryableUsernameError("approval_superuser_approve_required"))
	assert.False(t, isRetryableUsernameError("unknown"))
}

func TestSetUsername_PostsTheNameThroughTheCallersClient(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body SetUsernameRequest
		if r.Method != http.MethodPost || r.URL.Path != usernameURL || json.NewDecoder(r.Body).Decode(&body) != nil || body.Username != "alice" {
			t.Errorf("unexpected request: %s %s %+v", r.Method, r.URL.Path, body)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "u-1", "username": "alice"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	response, err := SetUsername(ac, "alice")

	require.NoError(t, err)
	assert.Equal(t, &SetUsernameResponse{ID: "u-1", Username: "alice"}, response)
}

func TestSetUsername_ReturnsTheRefusalInsteadOfExiting(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "user_username_in_use", "detail": "this username is already in use"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	response, err := SetUsername(ac, "alice")

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, codeUsernameInUse, code)
}

// usernameRefusal is a coded refusal of the username update.
type usernameRefusal struct{ code, hint string }

func (e usernameRefusal) Error() string       { return "permission denied (HTTP 403, code: " + e.code + ")" }
func (e usernameRefusal) ErrorCode() string   { return e.code }
func (e usernameRefusal) ErrorSource() string { return "" }
func (e usernameRefusal) ErrorHint() string   { return e.hint }

func TestUsernameFailure(t *testing.T) {
	t.Parallel()
	const hint = "Nobody but you could approve this request; it needs a superuser's approval."

	t.Run("a hinted refusal stops with the refusal kept", func(t *testing.T) {
		t.Parallel()
		refusal := usernameRefusal{code: codeUsernameApproval, hint: hint}
		warning, alreadySet, stop := UsernameFailure(refusal)
		assert.Empty(t, warning)
		assert.False(t, alreadySet)
		require.Error(t, stop)
		assert.Equal(t, "failed to set username: "+refusal.Error(), stop.Error())
		assert.NotContains(t, stop.Error(), "pending superuser approval")
		assert.Equal(t, hint, utils.RefusalHint(stop))
		code, _ := utils.ParseErrorResponse(stop)
		assert.Equal(t, codeUsernameApproval, code)
	})

	t.Run("the same code without a hint keeps the local message", func(t *testing.T) {
		t.Parallel()
		_, _, stop := UsernameFailure(usernameRefusal{code: codeUsernameApproval})
		require.Error(t, stop)
		assert.Equal(t, usernameErrors[codeUsernameApproval].message, stop.Error())
	})

	t.Run("a retryable code asks again", func(t *testing.T) {
		t.Parallel()
		warning, alreadySet, stop := UsernameFailure(usernameRefusal{code: codeUsernameInUse})
		assert.Equal(t, usernameErrors[codeUsernameInUse].message, warning)
		assert.False(t, alreadySet)
		assert.NoError(t, stop)
	})

	t.Run("an already set username carries on", func(t *testing.T) {
		t.Parallel()
		warning, alreadySet, stop := UsernameFailure(usernameRefusal{code: codeUsernameAlreadySet})
		assert.Empty(t, warning)
		assert.True(t, alreadySet)
		assert.NoError(t, stop)
	})

	t.Run("an unmapped refusal keeps its chain", func(t *testing.T) {
		t.Parallel()
		_, _, stop := UsernameFailure(usernameRefusal{code: "permission_denied", hint: hint})
		require.Error(t, stop)
		assert.Equal(t, hint, utils.RefusalHint(stop))
	})
}
