package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const serviceTokenID = "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b"

// serviceTokenServer answers the service token routes with respond and counts
// the requests it was sent.
func serviceTokenServer(t *testing.T, respond func(r *http.Request) (int, string)) (*client.AlpaconClient, *int) {
	t.Helper()
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		status, body := respond(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, &requests
}

func page(results string, next string) string {
	return `{"count": 2, "current": 1, "next": ` + next + `, "last": 2, "results": ` + results + `}`
}

// An id is looked up rather than passed through, so a personal API token's id
// is refused instead of being issued as a service token's.
func TestResolveServiceTokenID_ConfirmsAnID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "service token", status: http.StatusOK},
		{name: "not a service token", status: http.StatusNotFound, wantErr: ErrNotAServiceToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var path string
			ac, _ := serviceTokenServer(t, func(r *http.Request) (int, string) {
				path = r.URL.Path
				return tt.status, `{"id": "` + serviceTokenID + `", "name": "deploy-bot"}`
			})

			id, err := ResolveServiceTokenID(ac, " "+serviceTokenID+" ")

			assert.Equal(t, serviceTokenURL+serviceTokenID+"/", path)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, serviceTokenID, id)
		})
	}
}

func TestResolveServiceTokenID_ByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		results string
		wantID  string
		wantErr string
	}{
		{name: "exact match", results: `[{"id": "` + serviceTokenID + `", "name": "deploy"}]`, wantID: serviceTokenID},
		{name: "loose filter match is not taken", results: `[{"id": "` + serviceTokenID + `", "name": "deploy-prod"}]`, wantErr: "no service token found"},
		{name: "none", results: `[]`, wantErr: "no service token found"},
		{name: "two with the name", results: `[{"id": "a", "name": "deploy"}, {"id": "b", "name": "deploy"}]`, wantErr: "2 service tokens are named"},
		{name: "unusable id", results: `[{"id": "../x", "name": "deploy"}]`, wantErr: "unusable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, _ := serviceTokenServer(t, func(r *http.Request) (int, string) {
				assert.Equal(t, "deploy", r.URL.Query().Get("name"))
				return http.StatusOK, page(tt.results, "null")
			})

			id, err := ResolveServiceTokenID(ac, "deploy")

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, id)
		})
	}
}

func TestResolveServiceTokenID_RefusesABlankName(t *testing.T) {
	t.Parallel()
	ac, requests := serviceTokenServer(t, func(*http.Request) (int, string) { return http.StatusOK, page(`[]`, "null") })

	_, err := ResolveServiceTokenID(ac, "   ")

	require.ErrorIs(t, err, api.ErrBlankName)
	assert.Equal(t, 0, *requests)
}

func TestGetServiceTokenNames_WalksEveryPage(t *testing.T) {
	t.Parallel()
	ac, requests := serviceTokenServer(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("page") == "1" {
			return http.StatusOK, page(`[{"id": "a", "name": "deploy"}]`, "2")
		}
		return http.StatusOK, page(`[{"id": "b", "name": "backup"}]`, "null")
	})

	names, err := GetServiceTokenNames(ac)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "deploy", "b": "backup"}, names)
	assert.Equal(t, 2, *requests)
}
