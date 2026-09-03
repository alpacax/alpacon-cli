package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	return &AlpaconClient{
		HTTPClient:  &http.Client{},
		BaseURL:     baseURL,
		AccessToken: accessToken,
	}
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
// through setAccessToken the way refreshLocked does.
func stubTokenRenewal(t *testing.T, newToken string) *int {
	t.Helper()
	calls := 0
	swapTokenRenewal(t, func(ac *AlpaconClient) error {
		calls++
		ac.setAccessToken(newToken)
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
		ac.setAccessToken("fresh")
		return nil
	})

	ac := newBearerTestClient("https://example.com", "stale")
	renewed := make(chan bool, 1)
	go func() { renewed <- ac.renewAccessToken("stale") }()
	<-entered

	read := make(chan string, 1)
	go func() { read <- ac.accessToken() }()
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
