package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rawRoundTripFunc func(*http.Request) (*http.Response, error)

func newTestClient(baseURL string) *AlpaconClient {
	return &AlpaconClient{
		HTTPClient: &http.Client{},
		BaseURL:    baseURL,
		Token:      "test-token",
	}
}

func TestSendRequest_401SurfacesServerDetail(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "invalid token")
	assert.ErrorContains(t, err, "alpacon login")
}

func TestSendRequest_401WithoutBodyFallsBackToLoginHint(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendRequest_401EmptyJSONFallsBackToLoginHint(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "authentication failed")
	assert.NotContains(t, err.Error(), "{}")
}

func TestSendRequest_404ExposesStatusCode(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail": "not found"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err))
}

func TestSendRequest_404EmptyBodyExposesStatusCode(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err))
}

func TestSendRequest_404HTMLBodyExposesStatusCode(t *testing.T) {
	t.Parallel()
	// An old server/proxy without the endpoint may answer 404 with an HTML page;
	// the status must still reach callers so the whoami legacy fallback triggers.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<html><body>404 Not Found</body></html>`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err))
}

func TestSendRequest_TruncatedBodyKeepsStatusCode(t *testing.T) {
	t.Parallel()
	// A connection cut mid-body through a proxy: the headers already named the
	// status, so a caller can still tell an unreadable answer from a request that
	// never went out.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"detail":`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusOK, utils.HTTPStatusCode(err))
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestSendRequest_401ExposesStatusCode(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, utils.HTTPStatusCode(err))
}

func TestHTTPStatusCode_NonAPIErrorIsZero(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, utils.HTTPStatusCode(errors.New("boom")))
	assert.Equal(t, 0, utils.HTTPStatusCode(nil))
}

func TestSendRequest_429ExposesRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{name: "delta-seconds", retryAfter: "29", want: 29 * time.Second},
		{name: "padded delta-seconds", retryAfter: " 29 ", want: 29 * time.Second},
		{name: "no header", retryAfter: "", want: 0},
		{name: "zero is no hint", retryAfter: "0", want: 0},
		// The poll loop's own backoff covers this; guessing a date would stall it.
		{name: "HTTP-date is not parsed", retryAfter: "Wed, 21 Oct 2015 07:28:00 GMT", want: 0},
		// Parses fine, but seconds*time.Second would wrap past a Duration's range.
		{name: "too large for a Duration", retryAfter: "99999999999", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"detail": "Request was throttled."}`))
			}))
			defer ts.Close()

			ac := newTestClient(ts.URL)
			_, err := ac.SendGetRequest("/api/test/")
			require.Error(t, err)
			assert.Equal(t, http.StatusTooManyRequests, utils.HTTPStatusCode(err))
			assert.Equal(t, tt.want, utils.RetryAfter(err))
		})
	}
}

// A proxy-level throttle answers with its own HTML, which readJSONResponse rejects
// before the status check ever runs. The hint has to survive that path too.
func TestSendRequest_429WithNonJSONBodyExposesRetryAfter(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Retry-After", "29")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<html>rate limited</html>"))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusTooManyRequests, utils.HTTPStatusCode(err))
	assert.Equal(t, 29*time.Second, utils.RetryAfter(err))
}

// Upload shares the throttle, so it must tag the hint the same way sendRequest does.
func TestSendMultipartStreamRequest_429ExposesRetryAfter(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "29")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail": "Request was throttled."}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendMultipartStreamRequest("/api/test/", "multipart/form-data", bytes.NewReader([]byte("x")), -1)
	require.Error(t, err)
	assert.Equal(t, 29*time.Second, utils.RetryAfter(err))
}

func TestRetryAfter_ErrorWithoutHintIsZero(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Duration(0), utils.RetryAfter(errors.New("boom")))
	assert.Equal(t, time.Duration(0), utils.RetryAfter(nil))
}

func TestSendRequest_403SurfacesServerDetail(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "missing scope: sudo"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "missing scope: sudo")
}

func TestSendRequest_403PreservesWorkSessionCodeAndSource(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{
			"code": "work_session_required",
			"source": "command",
			"detail": "WorkSession required"
		}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "WorkSession required")

	code, source := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.WorkSessionRequired, code)
	assert.Equal(t, "command", source)
}

func TestSendRequest_403WithoutBodyFallsBackToGenericMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "permission denied")
}

func TestSendRequest_403EmptyDetailFallsBackToGenericMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": ""}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "permission denied")
	assert.NotContains(t, err.Error(), "detail:")
}

func TestSendRequest_403CodeWithoutDetailKeepsCodeSource(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code": "work_session_required", "source": "command"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	// Detail-less denial: generic message, but code/source must survive for exit-3 routing.
	require.ErrorContains(t, err, "permission denied")
	assert.NotContains(t, err.Error(), "work_session_required")
	code, source := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.WorkSessionRequired, code)
	assert.Equal(t, "command", source)
}

func TestSendRequest_403FieldErrorsRenderMessage(t *testing.T) {
	t.Parallel()
	// A code-only envelope with field_errors and no "detail" must still surface
	// the field error message rather than falling back to the generic floor.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","field_errors":{"non_field_errors":[{"code":"permission_denied","message":"You cannot edit this server."}]}}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "You cannot edit this server.")

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "permission_denied", code)
}

func TestSendRequest_403ACLDeniedExplainsTokenAccessControl(t *testing.T) {
	t.Parallel()
	// An ACL refusal (exec, websh, cp) arrives as a bare {"code": ...} 403—the
	// server's error handler emits nothing else. Without a mapping the user reads
	// the generic privileges message and never learns a token rule caused it.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code": "api_token_acl_not_allowed"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "token access control")
	require.ErrorContains(t, err, "alpacon token acl")
	// The no-raw-code contract: callers read the code via ErrorCode(), not the message.
	assert.NotContains(t, err.Error(), "api_token_acl_not_allowed")
	// A token missing a rule is not a stale session—never suggest re-login.
	assert.NotContains(t, err.Error(), "alpacon login")

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.APITokenACLNotAllowed, code)
}

func TestSendRequest_400ACLDeniedKeepsCodeWithoutAuthStatusMessage(t *testing.T) {
	t.Parallel()
	// The server still returns 400 for an ACL denial until alpacax/alpacon-server#2804
	// lands. checkAuthStatus only handles 401/403, so this body never reaches
	// authStatusCodeMessage—the code must still survive for callers that route on it.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "api_token_acl_not_allowed"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.APITokenACLNotAllowed, code)
	// The actionable message is the 403 mapping's job; a 400 must not borrow it,
	// or widening the status condition would go unnoticed.
	assert.NotContains(t, err.Error(), "token access control")
}

func TestSendRequest_CodeOnlyBodyRendersReadableMessage(t *testing.T) {
	t.Parallel()
	// Each code is paired with the status DRF actually sends it on.
	tests := []struct {
		name       string
		statusCode int
		code       string
		wantText   string
	}{
		{"api_not_found on 404", http.StatusNotFound, "api_not_found", "not found"},
		{"api_rate_limited on 429", http.StatusTooManyRequests, "api_rate_limited", "too many requests"},
		{"api_not_acceptable on 406", http.StatusNotAcceptable, "api_not_acceptable", "cannot produce a response"},
		{"api_unsupported_media_type on 415", http.StatusUnsupportedMediaType, "api_unsupported_media_type", "content type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"code": "` + tt.code + `"}`))
			}))
			defer ts.Close()

			ac := newTestClient(ts.URL)
			_, err := ac.SendGetRequest("/api/test/")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantText)
			assert.NotContains(t, err.Error(), "code: "+tt.code)

			code, _ := utils.ParseErrorResponse(err)
			assert.Equal(t, tt.code, code)
		})
	}
}

func TestSendRequest_UnknownCodeOnlyBodyNamesTheCodeInAFallback(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "some_future_code"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
	assert.Contains(t, err.Error(), "some_future_code")

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "some_future_code", code)
}

func TestSendRequest_400DetailWinsOverCode(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "api_not_found", "detail": "server-provided detail"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "server-provided detail", err.Error())
}

func TestSendRequest_FieldErrorsUnaffectedByCodeMapping(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"username": ["This field is required."], "non_field_errors": ["Invalid combination."]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username: This field is required.")
	assert.Contains(t, err.Error(), "Invalid combination.")
}

func TestSendRequest_CodeWithFieldErrorsKeepsFieldMessagesVisible(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "validation_error", "username": ["This field is required."]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username: This field is required.")
	assert.NotContains(t, err.Error(), "validation_error")

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "validation_error", code)
}

func TestSendRequest_ValidationBodyWithSourceFieldKeepsItsMessage(t *testing.T) {
	t.Parallel()
	// "source" is also a real serializer field name, so this is field errors, not the envelope.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"source": ["This field is required."], "name": ["This field is required."]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source: This field is required.")
	assert.Contains(t, err.Error(), "name: This field is required.")
}

func TestSendRequest_ValidationBodyWithCodeAndSourceFieldKeepsItsMessage(t *testing.T) {
	t.Parallel()
	// "source" is a real serializer field here too; a "code" alongside it must
	// not make isEnvelopeOnly mistake the field error for the refusal envelope.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "validation_error", "source": ["This field is required."]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source: This field is required.")
}

func TestSendRequest_RefusalBodyWithGateAndMissingIsEnvelopeOnly(t *testing.T) {
	t.Parallel()
	// gate/missing ride only on refusals; 402 keeps this off checkAuthStatus's 401/403 path.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "x", "gate": "g", "missing": ["a"]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
	assert.NotContains(t, err.Error(), "gate:")
	assert.NotContains(t, err.Error(), "missing:")
}

func TestSendRequest_RefusalBodyWithMissingButNoGateIsEnvelopeOnly(t *testing.T) {
	t.Parallel()
	// An unknown gate is dropped server-side, so "missing" can arrive alone.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "x", "missing": ["a"]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
	assert.NotContains(t, err.Error(), "missing:")
}

func TestSendRequest_RefusalBodyWithStringMissingIsEnvelopeOnly(t *testing.T) {
	t.Parallel()
	// "missing" may arrive as a single scope string rather than a list.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code": "api_rate_limited", "gate": "token_scope", "missing": "scope:server:create"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "too many requests—please try again later", err.Error())

	var apiErr interface{ ErrorMissing() []string }
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, []string{"scope:server:create"}, apiErr.ErrorMissing())
}

func TestSendRequest_FieldErrorsRenderSortedMessages(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "required", "field_errors": {"username": [{"code": "required", "message": "This field is required."}], "non_field_errors": [{"code": "invalid", "message": "Invalid combination."}], "websh_preferences.scrollback": [{"code":"max_value","message":"Ensure this value is less than or equal to 10000."}]}}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "Invalid combination.; username: This field is required.; websh_preferences.scrollback: Ensure this value is less than or equal to 10000.", err.Error())

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "required", code)
}

func TestSendRequest_MalformedFieldErrorsFallBackToCodeOnlyMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "required", "field_errors": {"username": ["not an object"], "empty": [{"code": "required", "message": ""}]}}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "request failed (code: required)", err.Error())
}

func TestSendRequest_DetailWinsOverFieldErrors(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail": "Custom message.", "code": "required", "field_errors": {"username": [{"code": "required", "message": "This field is required."}]}}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "Custom message.", err.Error())
}

func TestSendRequest_StringCodeFieldRendersAsFlatField(t *testing.T) {
	t.Parallel()
	// A legacy/direct view can send a flat field literally named "code".
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": ["This field is required."]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "code: This field is required.", err.Error())
}

func TestSendRequest_401OtherCodedDenialGetsGenericMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "some_other_denial"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "request denied by server", err.Error())

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "some_other_denial", code)
}

func TestSendRequest_401MissingOrFailedAuthCodeShowsLoginAgain(t *testing.T) {
	t.Parallel()
	for _, code := range []string{utils.AuthTokenMissing, utils.AuthAuthenticationFailed} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code": "` + code + `"}`))
			}))
			defer ts.Close()

			ac := newTestClient(ts.URL)
			_, err := ac.SendGetRequest("/api/test/")
			require.Error(t, err)
			assert.Equal(t, "authentication failed: please run 'alpacon login' again", err.Error())

			gotCode, _ := utils.ParseErrorResponse(err)
			assert.Equal(t, code, gotCode)
		})
	}
}

func TestSendRequest_403AuthTokenMissingCodeGetsGenericMessage(t *testing.T) {
	t.Parallel()
	for _, code := range []string{utils.AuthTokenMissing, utils.AuthAuthenticationFailed} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code": "` + code + `"}`))
			}))
			defer ts.Close()

			ac := newTestClient(ts.URL)
			_, err := ac.SendGetRequest("/api/test/")
			require.Error(t, err)
			assert.Equal(t, "permission denied: you do not have the required privileges for this action", err.Error())
			assert.NotContains(t, err.Error(), "alpacon login")

			gotCode, _ := utils.ParseErrorResponse(err)
			assert.Equal(t, code, gotCode)
		})
	}
}

func TestSendRequest_EmptyBodyUnaffectedByCodeMapping(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty error response")
}

func TestSendRequest_NonJSONBodyUnaffectedByCodeMapping(t *testing.T) {
	t.Parallel()
	// A non-JSON body on an error status is rejected by its Content-Type before
	// parseAPIErrorPayload ever runs, so the code mapping cannot apply here.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<html>not json</html>`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.NotContains(t, err.Error(), "code:")
}

func TestSendRequest_401MFARequiredCodeNoReLoginHint(t *testing.T) {
	t.Parallel()
	// Accessing root / a system account requires MFA: the server returns 401
	// with {"code": "auth_mfa_required"} and no detail string. This must not be
	// mislabeled as an authentication failure or suggest re-login—the user is
	// authenticated and only needs MFA. The code must survive for the MFA flow.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_mfa_required", "source": "websh"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "multi-factor authentication")
	assert.NotContains(t, err.Error(), "authentication failed")
	assert.NotContains(t, err.Error(), "alpacon login")

	code, source := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.AuthMFARequired, code)
	assert.Equal(t, "websh", source)
}

func TestSendRequest_401MFARequiredPrefersServerDetail(t *testing.T) {
	t.Parallel()
	// When the server also provides a human detail, surface it—still without a
	// re-login hint, since a coded 401 is not a stale token.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_mfa_required", "source": "websh", "detail": "MFA required to access root"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "MFA required to access root")
	assert.NotContains(t, err.Error(), "alpacon login")

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.AuthMFARequired, code)
}

func TestSendRequest_401CodedDenialNotMislabeledAsAuthFailure(t *testing.T) {
	t.Parallel()
	// Any coded 401 without a detail must not become "authentication failed" /
	// re-login; the code is preserved for downstream handling.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "some_policy_denial", "source": "command"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.NotContains(t, err.Error(), "authentication failed")
	assert.NotContains(t, err.Error(), "alpacon login")

	code, source := utils.ParseErrorResponse(err)
	assert.Equal(t, "some_policy_denial", code)
	assert.Equal(t, "command", source)
}

// The RBAC role gate and the token-scope gate now answer a refusal with one of
// these four codes instead of DRF's bare {"detail": ...}, and send no detail of
// their own—checkAuthStatus must keep "gate"/"missing" on the error (for
// cmd/iam's guidance table to read) and fall back to naming the missing
// scope(s) in the generic message when there is one.
func TestSendRequest_403CodedRefusalsPreserveGateAndMissing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		wantCode    string
		wantGate    string
		wantMissing []string
		wantMessage string
	}{
		{
			// The role gate's "missing" names a permission/role, not a scope, so the
			// fallback must not claim a "scope" is missing.
			name:        "role permission required names the single missing value without calling it a scope",
			body:        `{"code": "rbac_permission_required", "gate": "role", "missing": "role"}`,
			wantCode:    "rbac_permission_required",
			wantGate:    "role",
			wantMissing: []string{"role"},
			wantMessage: "permission denied: missing role",
		},
		{
			name:        "role object permission required without missing falls back generically",
			body:        `{"code": "rbac_object_permission_required", "gate": "role"}`,
			wantCode:    "rbac_object_permission_required",
			wantGate:    "role",
			wantMissing: nil,
			wantMessage: "permission denied: you do not have the required privileges for this action",
		},
		{
			name:        "token scope missing joins a list-form missing",
			body:        `{"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create", "scope:server:read"]}`,
			wantCode:    "api_token_scope_missing",
			wantGate:    "token_scope",
			wantMissing: []string{"scope:server:create", "scope:server:read"},
			wantMessage: "permission denied: missing scope scope:server:create, scope:server:read",
		},
		{
			name:        "token scope action unresolved without missing falls back generically",
			body:        `{"code": "api_token_scope_action_unresolved", "gate": "token_scope"}`,
			wantCode:    "api_token_scope_action_unresolved",
			wantGate:    "token_scope",
			wantMissing: nil,
			wantMessage: "permission denied: you do not have the required privileges for this action",
		},
		{
			// No "gate" at all—an unknown future code with a "missing" this CLI
			// cannot classify—must default to neutral wording, not scope wording.
			name:        "missing without a gate stays neutral",
			body:        `{"code": "some_future_code", "missing": "widget:create"}`,
			wantCode:    "some_future_code",
			wantGate:    "",
			wantMissing: []string{"widget:create"},
			wantMessage: "permission denied: missing widget:create",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			ac := newTestClient(ts.URL)
			_, err := ac.SendGetRequest("/api/test/")
			require.Error(t, err)
			assert.Equal(t, tt.wantMessage, err.Error())

			code, _ := utils.ParseErrorResponse(err)
			assert.Equal(t, tt.wantCode, code)

			gate, missing := utils.ParseErrorGateAndMissing(err)
			assert.Equal(t, tt.wantGate, gate)
			assert.Equal(t, tt.wantMissing, missing)
		})
	}
}

// Every one of these codes is documented as never carrying a "detail"—but if a
// future response does, the existing rule still applies: the server's human
// text wins over any client-side rendering of "missing".
func TestSendRequest_403DetailStillWinsOverMissing(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code": "api_token_scope_missing", "gate": "token_scope", "missing": "scope:server:create", "detail": "custom detail"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.ErrorContains(t, err, "custom detail")
	assert.NotContains(t, err.Error(), "missing scope")

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "api_token_scope_missing", code)
	gate, missing := utils.ParseErrorGateAndMissing(err)
	assert.Equal(t, "token_scope", gate)
	assert.Equal(t, []string{"scope:server:create"}, missing)
}

// A 405/429 never reaches checkAuthStatus (401/403 only), so parseAPIError is
// what must keep gate/missing for those—the same struct field, populated on a
// different path.
func TestSendRequest_429KeepsGateAndMissing(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"]}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, http.StatusTooManyRequests, utils.HTTPStatusCode(err))

	gate, missing := utils.ParseErrorGateAndMissing(err)
	assert.Equal(t, "token_scope", gate)
	assert.Equal(t, []string{"scope:server:create"}, missing)
}

func TestLoadCurrentUser_PopulatesFieldsAndCaches(t *testing.T) {
	t.Parallel()
	callCount := 0
	var requestedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CurrentUserResponse{
			Username:    " alice ",
			IsStaff:     true,
			IsSuperuser: false,
		})
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)

	err := ac.LoadCurrentUser()
	require.NoError(t, err)
	assert.Equal(t, "alice", ac.Username)
	assert.Equal(t, "staff", ac.Privileges)

	// Without the trailing slash Django's APPEND_SLASH answers a 301 and the client pays a second round trip.
	assert.Equal(t, "/api/iam/users/-/", requestedPath)

	_ = ac.LoadCurrentUser() // second call must be a no-op
	assert.Equal(t, 1, callCount, "LoadCurrentUser must hit the server exactly once")
}

func TestLoadCurrentUser_SuperuserPrivileges(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CurrentUserResponse{
			Username:    "bob",
			IsStaff:     true,
			IsSuperuser: true,
		})
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	require.NoError(t, ac.LoadCurrentUser())
	assert.Equal(t, "superuser", ac.Privileges)
}

func TestLoadCurrentUser_GeneralPrivileges(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CurrentUserResponse{
			Username:    "carol",
			IsStaff:     false,
			IsSuperuser: false,
		})
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	require.NoError(t, ac.LoadCurrentUser())
	assert.Equal(t, "general", ac.Privileges)
}

func TestLoadCurrentUser_401SurfacesServerDetail(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err := ac.LoadCurrentUser()
	require.ErrorContains(t, err, "invalid token")
	require.ErrorContains(t, err, "alpacon login")
	assert.Empty(t, ac.Username)
	assert.Empty(t, ac.Privileges)
}

func TestLoadCurrentUser_403ReturnsForbiddenError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err := ac.LoadCurrentUser()
	require.ErrorContains(t, err, "permission denied")
	assert.Empty(t, ac.Username)
	assert.Empty(t, ac.Privileges)
}

func TestLoadCurrentUser_InvalidJSONReturnsError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err := ac.LoadCurrentUser()
	require.Error(t, err)
	assert.Empty(t, ac.Username)
	assert.Empty(t, ac.Privileges)
}

func TestSendGetRequestForDownload_401ReturnsAuthError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequestForDownload("/api/test/")
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendGetRequestForDownload_403ReturnsForbiddenError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequestForDownload("/api/test/")
	assert.ErrorContains(t, err, "permission denied")
}

func TestSendMultipartStreamRequest_401ReturnsAuthError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendMultipartStreamRequest("/api/test/", mw.FormDataContentType(), &buf, int64(buf.Len()))
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendMultipartStreamRequest_200IsSuccess(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.Close()

	ac := newTestClient(ts.URL)
	body, err := ac.SendMultipartStreamRequest("/api/test/", mw.FormDataContentType(), &buf, int64(buf.Len()))
	require.NoError(t, err)
	assert.Equal(t, []byte(`{}`), body)
}

func TestSendMultipartStreamRequest_ReplaysFileBodyOnTemporaryRedirect(t *testing.T) {
	t.Parallel()
	var finalHit bool
	var uploadedContent string
	var finalContentLength int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/redirect/":
			http.Redirect(w, r, "/api/final/", http.StatusTemporaryRedirect)
		case "/api/final/":
			finalHit = true
			finalContentLength = r.ContentLength
			assert.Equal(t, http.MethodPost, r.Method)

			partReader, err := r.MultipartReader()
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			part, err := partReader.NextPart()
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			defer func() { _ = part.Close() }()

			content, err := io.ReadAll(part)
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			uploadedContent = string(content)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	tmp, err := os.CreateTemp(t.TempDir(), "multipart-*.body")
	require.NoError(t, err)
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	mw := multipart.NewWriter(tmp)
	part, err := mw.CreateFormFile("content", "pkg.whl")
	require.NoError(t, err)
	_, err = part.Write([]byte("package-content"))
	require.NoError(t, err)
	contentType := mw.FormDataContentType()
	require.NoError(t, mw.Close())

	size, err := tmp.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	_, err = tmp.Seek(0, io.SeekStart)
	require.NoError(t, err)

	ac := newTestClient(ts.URL)
	body, err := ac.SendMultipartStreamRequest("/api/redirect/", contentType, tmp, size)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{}`), body)
	assert.True(t, finalHit)
	assert.Equal(t, "package-content", uploadedContent)
	assert.Equal(t, size, finalContentLength)
}

func TestSendPostRequest_204IsSuccess(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	body, err := ac.SendPostRequest("/api/test/", struct{}{})
	require.NoError(t, err)
	assert.Empty(t, body)
}

func TestSendDeleteRequest_200IsSuccess(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	body, err := ac.SendDeleteRequest("/api/test/")
	require.NoError(t, err)
	assert.Equal(t, []byte(`{}`), body)
}

func TestLoadCurrentUser_ErrorIsCachedOnFailure(t *testing.T) {
	t.Parallel()
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err1 := ac.LoadCurrentUser()
	err2 := ac.LoadCurrentUser() // second call must return cached error without hitting server

	require.ErrorContains(t, err1, "invalid token")
	require.ErrorContains(t, err2, "invalid token")
	assert.Equal(t, 1, callCount, "LoadCurrentUser must hit the server exactly once even on failure")
}

// newBearerTestClient builds a client authenticated the way an Auth0 login
// leaves it: an access token, no legacy API key.
func newBearerTestClient(baseURL, accessToken string) *AlpaconClient {
	ac := &AlpaconClient{
		HTTPClient: &http.Client{},
		BaseURL:    baseURL,
	}
	ac.SetAccessToken(accessToken)
	return ac
}

// swapTokenRenewal points the refresh seam at renew for the rest of the test.
// The restore lives here so no test can leak a stub into the next one.
func swapTokenRenewal(t *testing.T, renew func(*AlpaconClient) error) {
	t.Helper()
	orig := refreshAccessToken
	t.Cleanup(func() { refreshAccessToken = orig })
	refreshAccessToken = renew
}

// stubTokenRenewal swaps the refresh seam for one that installs newToken and
// counts its runs. The seam runs with refreshMu held, so it installs the token
// through SetAccessToken the way refreshLocked does.
func stubTokenRenewal(t *testing.T, newToken string) *int {
	t.Helper()
	calls := 0
	swapTokenRenewal(t, func(ac *AlpaconClient) error {
		calls++
		ac.SetAccessToken(newToken)
		return nil
	})
	return &calls
}

// staleTokenHandler answers the code-less 401 alpacon-server sends once an
// access token expires: its Auth0 authenticator swallows the expired token
// (auth0/auth.py) and IsAuthenticatedOr401 raises DRF's NotAuthenticated, which
// no branch of the server's error_code_handler rewrites—so the body carries a
// detail and no code.
func staleTokenHandler(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"detail": "Authentication credentials were not provided."}`))
}

// An access token that expires mid-command is deterministic, not transient: a
// wait long enough to outlive it fails every time. A separate invocation
// refreshes at construction and succeeds, so a request in flight must renew too.
func TestSendGetRequest_RenewsAStaleTokenAndRetries(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	var sent []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("Authorization"))
		if len(sent) == 1 {
			staleTokenHandler(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "approved"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	body, err := ac.SendGetRequest("/api/test/")

	require.NoError(t, err)
	assert.JSONEq(t, `{"status": "approved"}`, string(body))
	assert.Equal(t, 1, *renewals)
	assert.Equal(t, []string{"Bearer stale", "Bearer fresh"}, sent, "the retry must carry the renewed token")
}

// The server rejects a stale token in its permission layer, before the view
// runs, so the first attempt changed nothing—but only a replayed body makes the
// retry the same request.
func TestSendPostRequest_ReplaysTheBodyAfterRenewal(t *testing.T) {
	stubTokenRenewal(t, "fresh")

	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if len(bodies) == 1 {
			staleTokenHandler(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendPostRequest("/api/test/", map[string]string{"purpose": "deploy"})

	require.NoError(t, err)
	require.Len(t, bodies, 2)
	assert.JSONEq(t, `{"purpose": "deploy"}`, bodies[1], "the replay must carry the original body")
}

// A coded 401 (MFA required, IP not allowed, token ACL) names what it wants,
// and a new token is not it. Renewing would retry a refusal unchanged.
func TestSendRequest_CodedUnauthorizedIsNotRenewed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_mfa_required"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, 0, *renewals, "a coded 401 is the server's decision, not a stale credential")
	assert.Equal(t, 1, requests)
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, utils.AuthMFARequired, code, "the code must still reach the MFA handler")
}

// auth_ip_not_allowed is a deliberate server refusal, not a stale token—unlike
// newTestClient's legacy path, a bearer client actually reaches
// isStaleCredential, so this is what proves it does not renew here either.
func TestSendRequest_CodedIPNotAllowedIsNotRenewed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_ip_not_allowed"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, 0, *renewals, "a coded 401 is the server's decision, not a stale credential")
	assert.Equal(t, 1, requests)
}

// TestSendGetRequest_RenewsACodedStaleTokenAndRetries covers auth_token_missing,
// which alpacon-server maps DRF's NotAuthenticated onto. A fresh token may clear
// it just as it may the code-less 401.
func TestSendGetRequest_RenewsACodedStaleTokenAndRetries(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	var sent []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("Authorization"))
		if len(sent) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code": "auth_token_missing"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "approved"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	body, err := ac.SendGetRequest("/api/test/")

	require.NoError(t, err)
	assert.JSONEq(t, `{"status": "approved"}`, string(body))
	assert.Equal(t, 1, *renewals)
	assert.Equal(t, []string{"Bearer stale", "Bearer fresh"}, sent, "the retry must carry the renewed token")
}

// TestSendRequest_CodedAuthenticationFailedIsNotRenewed covers a deliberate Auth0
// rejection. A new token will not move it, yet re-login is the user's only remedy.
func TestSendRequest_CodedAuthenticationFailedIsNotRenewed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_authentication_failed"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, "authentication failed: please run 'alpacon login' again", err.Error())
	assert.Equal(t, 0, *renewals, "an explicit refusal is not a stale credential a new token would move")
	assert.Equal(t, 1, requests)
}

// TestSendRequest_AuthTokenMissingOnLegacyTokenIsNotRenewed pins the renewal to
// renewedRequest's bearer-only check, since isStaleCredential accepts the code.
func TestSendRequest_AuthTokenMissingOnLegacyTokenIsNotRenewed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_token_missing"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, 0, *renewals)
	assert.Equal(t, 1, requests)
}

func TestSendGetRequest_StillCodedAfterRenewalDoesNotLoop(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code": "auth_token_missing"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, "authentication failed: please run 'alpacon login' again", err.Error())
	assert.Equal(t, 1, *renewals, "one replay only, even though the replay is coded stale too")
	assert.Equal(t, 2, requests)
}

// A service token or a legacy API key has no refresh token behind it, so a
// renewal would spend an Auth0 round trip to fail.
func TestSendRequest_LegacyTokenIsNotRenewed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		staleTokenHandler(w)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, 0, *renewals)
	assert.Equal(t, 1, requests)
}

// A proxy, a WAF or an mTLS gate can answer 401 before the request ever reaches
// alpacon-server, and what it writes is not the JSON every server error carries.
// No token this process can obtain moves that answer, so renewing on it would
// spend an Auth0 round trip and a config rewrite to be refused the same way.
func TestSendRequest_GatewayUnauthorizedIsNotRenewed(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "html error page", contentType: "text/html", body: "<html><body>401 Authorization Required</body></html>"},
		{name: "plain text", contentType: "text/plain", body: "client certificate required"},
		{name: "no body", contentType: "", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renewals := stubTokenRenewal(t, "fresh")

			requests := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			ac := newBearerTestClient(ts.URL, "stale")
			_, err := ac.SendGetRequest("/api/test/")

			require.Error(t, err)
			assert.Equal(t, 0, *renewals, "a 401 alpacon-server did not write is not a stale credential")
			assert.Equal(t, 1, requests, "a gateway refusal must not be replayed")
		})
	}
}

// A renewal this process cannot complete leaves it nothing better to send, so
// the caller reads the server's own rejection rather than the renewal's.
func TestSendRequest_RenewalFailureSurfacesTheOriginal401(t *testing.T) {
	swapTokenRenewal(t, func(*AlpaconClient) error { return errors.New("refresh token rejected") })

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		staleTokenHandler(w)
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	// The caller must read what the server said, not how the renewal failed.
	require.ErrorContains(t, err, "Authentication credentials were not provided.")
	assert.Equal(t, 1, requests, "a failed renewal must not replay the request")
}

// A token the server keeps rejecting is not a token this process can fix, so the
// retry happens once—never a loop against a 401 that will not move.
func TestSendRequest_RenewsAtMostOncePerRequest(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		staleTokenHandler(w)
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequest("/api/test/")

	require.Error(t, err)
	assert.Equal(t, 1, *renewals)
	assert.Equal(t, 2, requests)
}

// Two requests in flight share one expiry. The second must retry with what the
// first fetched instead of spending a second refresh-token grant on it.
func TestSendRequest_ConcurrentStaleRequestsRenewOnce(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer stale" {
			staleTokenHandler(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = ac.SendGetRequest("/api/test/")
		}()
	}
	wg.Wait()

	assert.NoError(t, errs[0])
	assert.NoError(t, errs[1])
	assert.Equal(t, 1, *renewals, "the second request must reuse the token the first fetched")
}

// A download bypasses sendRequest to hand the caller an open body, so it needs
// the same renewal rather than reporting a stale token as a download failure.
func TestSendGetRequestForDownload_RenewsAStaleTokenAndRetries(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	var sent []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("Authorization"))
		if len(sent) == 1 {
			staleTokenHandler(w)
			return
		}
		_, _ = w.Write([]byte("package-bytes"))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	resp, err := ac.SendGetRequestForDownload("/api/test/")

	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "package-bytes", string(body))
	assert.Equal(t, 1, *renewals)
	assert.Equal(t, []string{"Bearer stale", "Bearer fresh"}, sent)
}

// A download's 403 is a refusal, not a stale credential: it must reach the
// caller unretried, and carry its status the way every other API error does.
func TestSendGetRequestForDownload_ForbiddenIsNotRenewed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "no access"}`))
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	_, err := ac.SendGetRequestForDownload("/api/test/")

	require.Error(t, err)
	assert.Equal(t, 0, *renewals)
	assert.Equal(t, 1, requests)
	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err))
}

// A streamed upload that is not a file cannot be rewound, so the renewal must
// stop rather than replay a truncated body.
func TestSendMultipartStreamRequest_UnrewindableBodyIsNotReplayed(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = io.Copy(io.Discard, r.Body)
		staleTokenHandler(w)
	}))
	defer ts.Close()

	ac := newBearerTestClient(ts.URL, "stale")
	// A bare io.Reader gives net/http nothing to rewind with; an *os.File body
	// would, which is why SendMultipartStreamRequest sets GetBody for one.
	body := struct{ io.Reader }{strings.NewReader("payload")}
	_, err := ac.SendMultipartStreamRequest("/api/test/", "multipart/form-data", body, -1)

	require.Error(t, err)
	assert.Equal(t, 1, *renewals, "the token is renewed, but the request cannot be replayed")
	assert.Equal(t, 1, requests)
}

// The refresh-token grant is unbounded network I/O—no HTTP client on that path
// sets a timeout—so it must not hold the lock every other request takes to read
// the token. A background goroutine sharing this client (api/event/sudolistener.go
// spawns one per MFA frame) would otherwise stall the whole session on one slow
// Auth0 call.
func TestRenewAccessToken_DoesNotBlockTokenReads(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	swapTokenRenewal(t, func(ac *AlpaconClient) error {
		close(entered)
		<-release
		ac.SetAccessToken("fresh")
		return nil
	})

	ac := newBearerTestClient("https://example.com", "stale")
	renewed := make(chan bool, 1)
	go func() { renewed <- ac.renewAccessToken("stale") }()
	<-entered

	read := make(chan string, 1)
	go func() { read <- ac.AccessToken() }()
	select {
	case token := <-read:
		assert.Equal(t, "stale", token, "a read during the grant sees the token still in force")
	case <-time.After(2 * time.Second):
		t.Fatal("a token read blocked on an in-flight refresh")
	}

	close(release)
	assert.True(t, <-renewed, "the stubbed grant installs a token: %q", "fresh")
}

// Everything below cmd/ reads the workspace off the client, so dropping either
// field here leaves every MFA link the process builds naming no workspace.
func TestNewAlpaconAPIClient_PinsWorkspaceIdentityFromConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(
		"https://my-workspace.alpacon.io", "my-workspace", "alpat-token",
		"", "", "", "alpacon.io", 0, false,
	))

	ac, err := NewAlpaconAPIClient()

	require.NoError(t, err)
	assert.Equal(t, "https://my-workspace.alpacon.io", ac.BaseURL)
	assert.Equal(t, "my-workspace", ac.WorkspaceName)
}

// gorilla sends no User-Agent of its own and copies the header it is handed
// verbatim, so whatever these two build is exactly what reaches the server.
func TestSetWebsocketHeader_CarriesTheUserAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{
			name:      "the client's own",
			userAgent: "alpacon-cli/9.9.9",
			want:      "alpacon-cli/9.9.9",
		},
		{
			// Every client assembled outside NewAlpaconAPIClient, which is the only
			// place that fills the field in.
			name: "the default when the client carries none",
			want: utils.GetUserAgent(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac := &AlpaconClient{BaseURL: "https://my-workspace.alpacon.io", UserAgent: tt.userAgent}

			header := ac.SetWebsocketHeader()

			assert.Equal(t, tt.want, header.Get("User-Agent"))
			assert.Equal(t, "https://my-workspace.alpacon.io", header.Get("Origin"))
			assert.Empty(t, header.Get(ClientCapabilitiesHeader), "a plain dial claims no capability")
		})
	}
}

func TestSetWebsocketHeaderWithCapabilities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		capabilities []string
		want         string
	}{
		{
			name:         "one capability",
			capabilities: []string{CapabilityWebsocketReconnect},
			want:         CapabilityWebsocketReconnect,
		},
		{
			name:         "several are comma-separated",
			capabilities: []string{CapabilityWebsocketReconnect, "some-later-capability"},
			want:         CapabilityWebsocketReconnect + ", some-later-capability",
		},
		{
			// A caller with nothing to advertise must not send an empty header,
			// which a server could read as a claim of no capabilities at all.
			name: "none sends no header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac := &AlpaconClient{BaseURL: "https://my-workspace.alpacon.io", UserAgent: "alpacon-cli/9.9.9"}

			header := ac.SetWebsocketHeaderWithCapabilities(tt.capabilities...)

			assert.Equal(t, tt.want, header.Get(ClientCapabilitiesHeader))
			// The capabilities ride alongside what a plain dial already sends.
			assert.Equal(t, "alpacon-cli/9.9.9", header.Get("User-Agent"))
			assert.Equal(t, "https://my-workspace.alpacon.io", header.Get("Origin"))
		})
	}
}

// Issue #397: the token is written on one goroutine while others read it for the
// Authorization header. Nothing is stubbed, so the real grant runs. Serial because
// t.Setenv panics alongside t.Parallel().
func TestRefreshTokenRacesConcurrentRequests(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	mux.HandleFunc("/api/auth/env/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth0":{"method":"auth0","client_id":"cli","domain":"` + strings.TrimPrefix(ts.URL, "https://") + `"}}`))
	})
	mux.HandleFunc("/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/api/test/", func(w http.ResponseWriter, r *http.Request) {
		// Without this the torn header only shows up under -race.
		if got := r.Header.Get("Authorization"); got != "Bearer stale" && got != "Bearer fresh" {
			t.Errorf("Authorization header was torn: %q", got)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	require.NoError(t, config.CreateConfig(
		ts.URL, "ws", "", "", "stale", "r1", "alpacon.io", 0, false,
	))

	// A stalled round trip must fail the test, not hang it until the go test deadline.
	httpClient := ts.Client()
	httpClient.Timeout = 10 * time.Second

	ac := &AlpaconClient{HTTPClient: httpClient, BaseURL: ts.URL}
	ac.SetAccessToken("stale")

	const readers, reads, refreshes = 8, 50, 20
	errs := make(chan error, readers*reads+refreshes)

	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range reads {
				_, err := ac.SendGetRequest("/api/test/")
				errs <- err
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range refreshes {
			errs <- ac.RefreshToken()
		}
	}()
	wg.Wait()
	close(errs)

	// A deadlock surfaces as an unanswered request, not as a race report.
	for err := range errs {
		require.NoError(t, err)
	}
}

// TestPlanLimitMessage covers the client classification (§1.3) and the
// CLI/MCP/alpamon message template (§1.6) of the paywall wave's wire contract.
// workspaceURL is exercised directly here—an httptest server's URL is always
// self-hosted-shaped, so the acme.us1.alpacon.io billing-link case can only be
// reached by calling planLimitMessage itself rather than through a full
// SendGetRequest round trip.
func TestPlanLimitMessage(t *testing.T) {
	t.Parallel()

	const wantServerLimitMessage = "plan limit reached: servers. Remove one you no longer use, or upgrade the plan. " +
		"If this host was registered before, delete the old server entry first, then retry. " +
		"Upgrade: https://alpacon.io/acme/settings/billing. Talk to us: https://www.alpacax.com/alpacon/pricing"

	tests := []struct {
		name         string
		axis         string
		code         string
		gate         string
		retryAfter   time.Duration
		workspaceURL string
		wantOK       bool
		wantMessage  string
	}{
		{
			name:         "full envelope renders the billing link",
			axis:         "server",
			code:         "server_limit_exceeded",
			gate:         "plan",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       true,
			wantMessage:  wantServerLimitMessage,
		},
		{
			// rule 2: an older server (or an agent talking to one) sends no
			// "gate"/"axis" at all—only the legacy code.
			name:         "gate-less legacy code renders the same message",
			code:         "server_limit_exceeded",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       true,
			wantMessage:  wantServerLimitMessage,
		},
		{
			name:         "self-hosted host prints words instead of a URL",
			axis:         "server",
			code:         "server_limit_exceeded",
			gate:         "plan",
			workspaceURL: "https://alpacon.mycompany.internal",
			wantOK:       true,
			wantMessage: "plan limit reached: servers. Remove one you no longer use, or upgrade the plan. " +
				"If this host was registered before, delete the old server entry first, then retry. " +
				"Upgrade: Settings → Billing in your Alpacon console. Talk to us: https://www.alpacax.com/alpacon/pricing",
		},
		{
			name:         "monthly axis with Retry-After renders the reset day count",
			axis:         "websh",
			code:         "websh_limit_exceeded",
			gate:         "plan",
			retryAfter:   864000 * time.Second,
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       true,
			wantMessage: "plan limit reached: Websh hours this month. Resets in 10 days. " +
				"Upgrade: https://alpacon.io/acme/settings/billing. Talk to us: https://www.alpacax.com/alpacon/pricing",
		},
		{
			// rule 3: gate "plan" with no axis is a feature lock, not a limit—no
			// axis name in the opening sentence.
			name:         "feature lock has no axis to name",
			code:         "workspace_plan_update_required",
			gate:         "plan",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       true,
			wantMessage: "This action needs a higher plan. Upgrade: https://alpacon.io/acme/settings/billing. " +
				"Talk to us: https://www.alpacax.com/alpacon/pricing",
		},
		{
			// rule 4: some other gate (here, an RBAC role gate) is not a plan
			// refusal at all—today's generic message stays.
			name:         "an unrelated gate is rule 4, unmatched",
			code:         "rbac_permission_required",
			gate:         "role",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       false,
		},
		{
			name:         "a gate-less unknown code is rule 4, unmatched",
			code:         "something_else",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       false,
		},
		{
			// workspace_free_limit_exceeded is deliberately absent from
			// legacyPlanLimitAxis: a gate-less 402 with this code may be
			// alpacon-account's own Free-workspace refusal, not a plan limit.
			name:         "workspace axis is excluded from the legacy code map",
			code:         "workspace_free_limit_exceeded",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			message, ok := planLimitMessage(tt.axis, tt.code, tt.gate, tt.retryAfter, tt.workspaceURL)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantMessage, message)
			}
		})
	}
}

// TestConsoleLabel mirrors cmd/login.go's TestIsCloudWorkspaceURL cases:
// consoleLabel repeats that function's canonical-form definition (client
// cannot import cmd), so a host it accepts or rejects must match.
func TestConsoleLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		workspaceURL string
		wantLabel    string
		wantOK       bool
	}{
		{
			name:         "canonical cloud URL",
			workspaceURL: "https://acme.us1.alpacon.io",
			wantLabel:    "acme",
			wantOK:       true,
		},
		{
			name:         "mixed-case base domain is still canonical",
			workspaceURL: "https://acme.us1.Alpacon.io",
			wantLabel:    "acme",
			wantOK:       true,
		},
		{
			name:         "mixed-case scheme is still canonical",
			workspaceURL: "HTTPS://acme.us1.alpacon.io",
			wantLabel:    "acme",
			wantOK:       true,
		},
		{
			name:         "cloud host without a scheme normalizes to canonical",
			workspaceURL: "acme.us1.alpacon.io",
			wantLabel:    "acme",
			wantOK:       true,
		},
		{
			name:         "self-hosted URL is not cloud",
			workspaceURL: "https://alpacon.example.com",
			wantOK:       false,
		},
		{
			name:         "http cloud-shaped URL is non-canonical",
			workspaceURL: "http://acme.us1.alpacon.io",
			wantOK:       false,
		},
		{
			// A self-hosted endpoint whose hostname merely resembles the cloud
			// pattern (e.g. a proxy or tunnel on a nonstandard port) must not
			// receive an unverified cloud billing link.
			name:         "cloud-shaped URL with a port is non-canonical",
			workspaceURL: "https://acme.us1.alpacon.io:8443",
			wantOK:       false,
		},
		{
			name:         "cloud-shaped URL with a path is non-canonical",
			workspaceURL: "https://acme.us1.alpacon.io/foo",
			wantOK:       false,
		},
		{
			name:         "bare base domain has no workspace label",
			workspaceURL: "https://us1.alpacon.io",
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			label, ok := consoleLabel(tt.workspaceURL)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantLabel, label)
			}
		})
	}
}

// TestSendRequest_PlanLimitEnvelopeRendersReadableMessage pins the fix for
// what today's client renders as garbled field errors: before axis/next joined
// the envelope isEnvelopeOnly rejected this body on sight of "axis", and the
// fallback field-validation renderer turned it into
// "axis: server; gate: plan; next: ...". This is the exact shape release order
// step 5 in the paywall wave plan calls out as today's (cosmetic) CLI output.
func TestSendRequest_PlanLimitEnvelopeRendersReadableMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "server_limit_exceeded", "gate": "plan", "axis": "server", "next": "/api/workspaces/workspaces/abc/entitlements/"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "axis:")
	assert.NotContains(t, err.Error(), "gate:")
	assert.NotContains(t, err.Error(), "next:")
	assert.Contains(t, err.Error(), "plan limit reached: servers.")
	// ts.URL is never <label>.<region>.alpacon.io, so this exercises the
	// self-hosted words branch through the full round trip.
	assert.Contains(t, err.Error(), "Settings → Billing in your Alpacon console")
	assert.Equal(t, http.StatusPaymentRequired, utils.HTTPStatusCode(err))

	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "server_limit_exceeded", code)
}

func TestSendRequest_PlanLimitGatelessLegacyCodeSameClassification(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "server_limit_exceeded"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan limit reached: servers.")
}

func TestSendRequest_PlanLimitFeatureLockRendersNeedsHigherPlan(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "workspace_plan_update_required", "gate": "plan"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t,
		"This action needs a higher plan. Upgrade: Settings → Billing in your Alpacon console. "+
			"Talk to us: https://www.alpacax.com/alpacon/pricing",
		err.Error())
}

func TestSendRequest_PlanLimitMonthlyAxisRendersResetDays(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "864000")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "websh_limit_exceeded", "gate": "plan", "axis": "websh"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Resets in 10 days.")
	// The rewrite must not disturb the Retry-After hint the poll loops read.
	assert.Equal(t, 864000*time.Second, utils.RetryAfter(err))
}

func TestSendRequest_PlanLimitGatelessWorkspaceAxisCodeNotReclassified(t *testing.T) {
	t.Parallel()
	// workspace_free_limit_exceeded is deliberately excluded from the legacy
	// axis map (see TestPlanLimitMessage); a gate-less 402 with this code keeps
	// today's generic rendering rather than being repainted as a plan limit.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"code": "workspace_free_limit_exceeded"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "request failed (code: workspace_free_limit_exceeded)", err.Error())
}

// TestSendRequest_NonPlanLimitStatusKeepsAxisAndNextAsFieldErrors guards
// isEnvelopeOnly's statusCode gate: axis/next are 402 plan-limit fields only
// (§1.1), so a validation response on any other status that happens to carry
// same-named fields must still render their messages rather than being
// swallowed into a bare code-only message.
func TestSendRequest_NonPlanLimitStatusKeepsAxisAndNextAsFieldErrors(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "validation_error", "axis": "This field is required.", "next": "Must be a valid choice."}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	require.Error(t, err)
	assert.Equal(t, "axis: This field is required.; next: Must be a valid choice.", err.Error())
}

func (f rawRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func rawTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{Status: http.StatusText(status), StatusCode: status, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewBufferString(body))}
}

func TestSendRawRequest_PreservesResponse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		ct     string
		body   string
	}{
		{"json success", http.StatusOK, "application/json", `{"ok":true}`},
		{"json refusal", http.StatusNotFound, "application/json", `{"detail":"missing"}`},
		{"html error", http.StatusInternalServerError, "text/html", `<h1>error</h1>`},
		{"csv success", http.StatusOK, "text/csv", "a,b\n1,2\n"},
		{"empty success", http.StatusNoContent, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := newTestClient("https://workspace.example")
			ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				return rawTestResponse(tc.status, tc.ct, tc.body), nil
			})

			resp, err := ac.SendRawRequest(http.MethodGet, "/api/test", nil, nil)

			require.NoError(t, err)
			assert.Equal(t, tc.status, resp.StatusCode)
			assert.Equal(t, tc.ct, resp.Header.Get("Content-Type"))
			assert.Equal(t, tc.body, string(resp.Body))
		})
	}
}

func TestSendRawRequest_RenewsAndReplaysBody(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")
	var bodies, tokens []string
	ac := newBearerTestClient("https://workspace.example", "stale")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(bodies) == 1 {
			return rawTestResponse(http.StatusUnauthorized, "application/json", `{"detail":"expired"}`), nil
		}
		return rawTestResponse(http.StatusCreated, "application/json", `{"ok":true}`), nil
	})

	resp, err := ac.SendRawRequest(http.MethodPost, "/api/test", bytes.NewReader([]byte(`{"a":1}`)), nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, 1, *renewals)
	assert.Equal(t, []string{`{"a":1}`, `{"a":1}`}, bodies)
	assert.Equal(t, []string{"Bearer stale", "Bearer fresh"}, tokens)
}

func TestValidateRawRequestHeaders_ContentLengthWithoutBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
		value  string
		bad    bool
	}{
		{"post zero accepted", http.MethodPost, "0", false},
		{"put zero accepted", http.MethodPut, "0", false},
		{"patch zero accepted", http.MethodPatch, "0", false},
		{"delete zero accepted", http.MethodDelete, "0", false},
		{"get zero still rejected", http.MethodGet, "0", true},
		{"head zero still rejected", http.MethodHead, "0", true},
		{"post nonzero rejected", http.MethodPost, "5", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := make(http.Header)
			header.Set("Content-Length", tc.value)

			err := ValidateRawRequestHeaders(tc.method, header, 0, false)

			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSendRawRequest_RejectsContentLengthForUnknownBodySize(t *testing.T) {
	t.Parallel()
	requests := 0
	ac := newTestClient("https://workspace.example")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return rawTestResponse(http.StatusNoContent, "", ""), nil
	})
	header := make(http.Header)
	header.Set("Content-Length", "0")

	_, err := ac.SendRawRequest(http.MethodPost, "/x", io.NopCloser(strings.NewReader("x")), header)

	require.Error(t, err)
	assert.Equal(t, 0, requests)
}

func TestSendRawRequest_CodedUnauthorizedDoesNotRenew(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")
	requests := 0
	ac := newBearerTestClient("https://workspace.example", "stale")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return rawTestResponse(http.StatusUnauthorized, "application/json", `{"code":"auth_mfa_required"}`), nil
	})

	resp, err := ac.SendRawRequest(http.MethodGet, "/api/test", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.JSONEq(t, `{"code":"auth_mfa_required"}`, string(resp.Body))
	assert.Equal(t, 0, *renewals)
	assert.Equal(t, 1, requests)
}

func TestSendRawRequest_DoesNotFollowRedirectOffWorkspaceHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		baseURL       string
		location      string
		rewriteToEvil bool // delegated CheckRedirect approves, then mutates next.URL to evil.example
	}{
		{name: "redirects straight off-host", location: "https://evil.example/x"},
		{name: "delegated check rewrites off-host after approving", location: "https://workspace.example/y", rewriteToEvil: true},
		{name: "same host scheme downgrade", location: "http://workspace.example/y"},
		{name: "same host port change", baseURL: "https://workspace.example:8443", location: "https://workspace.example:9443/y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			baseURL := tc.baseURL
			if baseURL == "" {
				baseURL = "https://workspace.example"
			}
			base, err := url.Parse(baseURL)
			require.NoError(t, err)
			otherRequests := 0
			ac := newBearerTestClient(baseURL, "secret")
			if tc.rewriteToEvil {
				ac.HTTPClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
					rewritten, err := url.Parse("https://evil.example/x")
					require.NoError(t, err)
					*next.URL = *rewritten
					return nil
				}
			}
			ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != base.Host || r.URL.Scheme != base.Scheme {
					otherRequests++
					return rawTestResponse(http.StatusOK, "text/plain", "external"), nil
				}
				response := rawTestResponse(http.StatusFound, "text/plain", "redirect")
				response.Header.Set("Location", tc.location)
				return response, nil
			})

			response, err := ac.SendRawRequest(http.MethodGet, "/x", nil, nil)

			require.NoError(t, err)
			assert.Equal(t, http.StatusFound, response.StatusCode)
			assert.Equal(t, 0, otherRequests)
		})
	}
}

func TestNormalizeRawEndpoint_RestrictsToWorkspacePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		want string
		bad  bool
	}{
		{"relative", "api/v1/x", "/api/v1/x", false},
		{"absolute path", "/api/v1/x?foo=1", "/api/v1/x?foo=1", false},
		{"url", "https://evil.example/x", "", true},
		{"network path", "//evil.example/x", "", true},
		{"scheme path", "http:/x", "", true},
		{"query value with scheme is not a scheme", "/api/hooks?callback=https://example.com/result", "/api/hooks?callback=https://example.com/result", false},
		{"fragment", "/x#frag", "", true},
		{"empty fragment", "/x#", "", true},
		{"fragment after query", "/x?a=1#", "", true},
		{"encoded hash in path", "/x%23frag", "/x%23frag", false},
		{"encoded hash in query value", "/x?a=b%23c", "/x?a=b%23c", false},
		{"raw space in query", "/x?a=1 b", "", true},
		{"bad percent-escape in query", "/x?a=%zz", "", true},
		{"encoded space in query", "/x?a=1%20b", "/x?a=1%20b", false},
		{"plus in query", "/x?a=1+b", "/x?a=1+b", false},
		{"semicolon in query", "/x?a=1;b=2", "/x?a=1;b=2", false},
		{"raw space in path is left for http.NewRequest to encode", "/x y", "/x y", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeRawEndpoint(tc.path)

			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSendRawRequest_AcceptsCallbackQueryWithSchemeAndKeepsWorkspaceHost(t *testing.T) {
	t.Parallel()
	var gotHost, gotQuery string
	ac := newTestClient("https://workspace.example")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		gotQuery = r.URL.RawQuery
		return rawTestResponse(http.StatusOK, "application/json", `{}`), nil
	})

	_, err := ac.SendRawRequest(http.MethodGet, "/api/hooks?callback=https://example.com/result", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "workspace.example", gotHost)
	assert.Equal(t, "callback=https://example.com/result", gotQuery)
}

func TestSendRawRequest_EncodedOrBackslashSlashesDoNotChangeRequestHost(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"/api/x%2F%2Fevil", `/api/x\\evil`, `/api\evil.example/x`} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			var gotHost string
			ac := newTestClient("https://workspace.example")
			ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				gotHost = r.URL.Host
				return rawTestResponse(http.StatusOK, "application/json", `{}`), nil
			})

			_, err := ac.SendRawRequest(http.MethodGet, endpoint, nil, nil)

			require.NoError(t, err)
			assert.Equal(t, "workspace.example", gotHost)
		})
	}
}

func TestSendRawRequest_CustomAuthorizationDoesNotReplayWithManagedToken(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh-managed")
	var tokens, bodies []string
	ac := newBearerTestClient("https://workspace.example", "managed")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(tokens) == 1 {
			return rawTestResponse(http.StatusUnauthorized, "application/json", `{"detail":"expired"}`), nil
		}
		return rawTestResponse(http.StatusOK, "application/json", `{"unexpected":true}`), nil
	})

	response, err := ac.SendRawRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"value":1}`)), http.Header{"Authorization": []string{"Bearer custom-token"}})

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Equal(t, []string{"Bearer custom-token"}, tokens)
	assert.Equal(t, []string{`{"value":1}`}, bodies)
	assert.Equal(t, 0, *renewals)
}

func TestSendRawRequest_RenewalPreservesCustomUserAgent(t *testing.T) {
	stubTokenRenewal(t, "fresh")
	var agents []string
	ac := newBearerTestClient("https://workspace.example", "stale")
	ac.UserAgent = "default-agent"
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		agents = append(agents, r.Header.Get("User-Agent"))
		if len(agents) == 1 {
			return rawTestResponse(http.StatusUnauthorized, "application/json", `{"detail":"expired"}`), nil
		}
		return rawTestResponse(http.StatusOK, "application/json", `{}`), nil
	})

	response, err := ac.SendRawRequest(http.MethodGet, "/x", nil, http.Header{"User-Agent": []string{"custom-agent"}})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"custom-agent", "custom-agent"}, agents)
}
