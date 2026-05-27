package client

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestClient(baseURL string) *AlpaconClient {
	return &AlpaconClient{
		HTTPClient: &http.Client{},
		BaseURL:    baseURL,
		Token:      "test-token",
	}
}

func TestSendRequest_401SurfacesServerDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "invalid token")
	assert.ErrorContains(t, err, "alpacon login")
}

func TestSendRequest_401WithoutBodyFallsBackToLoginHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendRequest_403SurfacesServerDetail(t *testing.T) {
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

func TestSendRequest_403WithoutBodyFallsBackToGenericMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "permission denied")
}

func TestLoadCurrentUser_PopulatesFieldsAndCaches(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckPrivilegesResponse{
			Username:    " alice ",
			IsStaff:     true,
			IsSuperuser: false,
		})
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)

	err := ac.LoadCurrentUser()
	assert.NoError(t, err)
	assert.Equal(t, "alice", ac.Username)
	assert.Equal(t, "staff", ac.Privileges)

	_ = ac.LoadCurrentUser() // second call must be a no-op
	assert.Equal(t, 1, callCount, "LoadCurrentUser must hit the server exactly once")
}

func TestLoadCurrentUser_SuperuserPrivileges(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckPrivilegesResponse{
			Username:    "bob",
			IsStaff:     true,
			IsSuperuser: true,
		})
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	assert.NoError(t, ac.LoadCurrentUser())
	assert.Equal(t, "superuser", ac.Privileges)
}

func TestLoadCurrentUser_GeneralPrivileges(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckPrivilegesResponse{
			Username:    "carol",
			IsStaff:     false,
			IsSuperuser: false,
		})
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	assert.NoError(t, ac.LoadCurrentUser())
	assert.Equal(t, "general", ac.Privileges)
}

func TestLoadCurrentUser_401SurfacesServerDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err := ac.LoadCurrentUser()
	assert.ErrorContains(t, err, "invalid token")
	assert.ErrorContains(t, err, "alpacon login")
	assert.Empty(t, ac.Username)
	assert.Empty(t, ac.Privileges)
}

func TestLoadCurrentUser_403ReturnsForbiddenError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err := ac.LoadCurrentUser()
	assert.ErrorContains(t, err, "permission denied")
	assert.Empty(t, ac.Username)
	assert.Empty(t, ac.Privileges)
}

func TestLoadCurrentUser_InvalidJSONReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	err := ac.LoadCurrentUser()
	assert.Error(t, err)
	assert.Empty(t, ac.Username)
	assert.Empty(t, ac.Privileges)
}

func TestSendGetRequestForDownload_401ReturnsAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequestForDownload("/api/test/")
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendGetRequestForDownload_403ReturnsForbiddenError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequestForDownload("/api/test/")
	assert.ErrorContains(t, err, "permission denied")
}

func TestSendMultipartRequest_401ReturnsAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendMultipartRequest("/api/test/", mw, buf)
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendMultipartRequest_200IsSuccess(t *testing.T) {
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
	body, err := ac.SendMultipartRequest("/api/test/", mw, buf)
	assert.NoError(t, err)
	assert.Equal(t, []byte(`{}`), body)
}

func TestLoadCurrentUser_ErrorIsCachedOnFailure(t *testing.T) {
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

	assert.ErrorContains(t, err1, "invalid token")
	assert.ErrorContains(t, err2, "invalid token")
	assert.Equal(t, 1, callCount, "LoadCurrentUser must hit the server exactly once even on failure")
}
