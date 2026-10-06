package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWorkspaceID(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/workspaces/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "uuid-1", "schema_name": "ws-alpha"},
				{"id": "uuid-2", "schema_name": "ws-beta"},
			},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	id, err := GetWorkspaceID(ac, ts.URL, "ws-beta")
	require.NoError(t, err)
	assert.Equal(t, "uuid-2", id)
}

func TestGetWorkspaceID_NotFound(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "uuid-1", "schema_name": "ws-alpha"},
			},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := GetWorkspaceID(ac, ts.URL, "ws-unknown")
	require.ErrorContains(t, err, "not found")
}

func TestGetUsageEstimate(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/workspaces/uuid-123/estimate/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"currency": "KRW",
			"billing_period": map[string]any{
				"start":      "2026-04-01T00:00:00Z",
				"end":        "2026-04-30T23:59:59Z",
				"total_days": float64(30),
			},
			"subscription": map[string]any{
				"product_name": "Alpacon Core",
				"plan_name":    "Essentials",
				"sub_total":    "360000",
			},
			"services": map[string]any{},
			"metadata": nil,
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	estimate, err := GetUsageEstimate(ac, ts.URL, "uuid-123")
	require.NoError(t, err)
	assert.Equal(t, "KRW", estimate.Currency)
	assert.Equal(t, 30, estimate.BillingPeriod.TotalDays)
	assert.Equal(t, "Alpacon Core", estimate.Subscription.ProductName)
}

func TestGetUsageEstimate_ServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := GetUsageEstimate(ac, ts.URL, "uuid-123")
	assert.Error(t, err)
}

// Logged in through a URL whose slug was renamed, the workspace is still known
// to the payment API by its schema_name, so the client built from that config
// must find it.
func TestGetWorkspaceID_FindsWorkspaceAfterSlugRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, config.ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{
		"workspace_url": "https://new-slug.us1.alpacon.io",
		"workspace_name": "new-slug",
		"schema_name": "frozen-schema",
		"token": "alpat-token"
	}`), 0o600))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "uuid-other", "schema_name": "other"},
				{"id": "uuid-1", "schema_name": "frozen-schema"},
			},
		})
	}))
	defer ts.Close()

	ac, err := client.NewAlpaconAPIClient()
	require.NoError(t, err)
	ac.HTTPClient = ts.Client()

	id, err := GetWorkspaceID(ac, ts.URL, ac.WorkspaceName)

	require.NoError(t, err)
	assert.Equal(t, "uuid-1", id)
}
