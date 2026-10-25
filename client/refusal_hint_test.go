package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sendRefusal answers one request with status and body and returns the error
// the client made of it.
func sendRefusal(t *testing.T, status int, body string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	_, err := newTestClient(ts.URL).SendGetRequest("/api/test/")
	require.Error(t, err)
	return err
}

func TestSendRequest_RefusalHintIsCarriedOnEveryRefusalStatus(t *testing.T) {
	t.Parallel()
	const hint = "This token does not hold a scope this action needs; use a token that has the scope, then retry."
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "403 through the auth status path",
			status: http.StatusForbidden,
			body:   `{"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"], "hint": "` + hint + `"}`,
		},
		{
			name:   "402 plan refusal",
			status: http.StatusPaymentRequired,
			body:   `{"code": "server_limit_exceeded", "gate": "plan", "axis": "server", "hint": "` + hint + `"}`,
		},
		{
			name:   "405 refusal",
			status: http.StatusMethodNotAllowed,
			body:   `{"code": "workspace_put_info_disabled", "hint": "` + hint + `"}`,
		},
		{
			name:   "429 refusal",
			status: http.StatusTooManyRequests,
			body:   `{"code": "api_rate_limited", "gate": "rate", "hint": "` + hint + `"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, tt.status, tt.body)

			assert.Equal(t, hint, utils.RefusalHint(err))
			assert.Equal(t, []utils.NextAction{{Description: hint}}, utils.RefusalNextActions(err))
			// The hint is part of the envelope, so it must not leak into the
			// message as if it were a field error.
			assert.NotContains(t, err.Error(), "hint:")
			assert.NotContains(t, err.Error(), "gate:")
		})
	}
}

func TestSendRequest_RefusalWithoutHintKeepsTodaysOutput(t *testing.T) {
	t.Parallel()
	// A server that predates the field sends none: no guidance is added and the
	// message is what it was.
	err := sendRefusal(t, http.StatusForbidden, `{"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"]}`)

	assert.Empty(t, utils.RefusalHint(err))
	assert.Nil(t, utils.RefusalNextActions(err))
	assert.Equal(t, "permission denied: missing scope scope:server:create", err.Error())
}

func TestSendRequest_HintOutsideARefusalIsAFieldError(t *testing.T) {
	t.Parallel()
	// A 400 field named "hint" is a validation message about the request, not
	// the server's guidance.
	err := sendRefusal(t, http.StatusBadRequest, `{"hint": ["This field may not be blank."]}`)

	assert.Empty(t, utils.RefusalHint(err))
	assert.Equal(t, "hint: This field may not be blank.", err.Error())
}

func TestSendRequest_RefusalHintIsSanitized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		hint string
		want string
	}{
		{
			name: "control characters and escapes are removed",
			hint: `Open a work\n session.\u001b[2K\r\t\u009b\u202e`,
			want: "Open a work session.",
		},
		{
			name: "an over-long hint is cut to the byte cap",
			hint: strings.Repeat("x", 300),
			want: strings.Repeat("x", utils.MaxServerHintBytes),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendRefusal(t, http.StatusForbidden, `{"code": "permission_denied", "hint": "`+tt.hint+`"}`)

			assert.Equal(t, tt.want, utils.RefusalHint(err))
		})
	}
}
