package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestGetPaymentAPIBaseURL(t *testing.T) {
	tests := []struct {
		name         string
		workspaceURL string
		expected     string
	}{
		{"production AP region", "https://myws.ap1.alpacon.io", paymentAPIProdURL},
		{"production US region", "https://myws.us1.alpacon.io", paymentAPIProdURL},
		{"staging dev region", "https://myws.dev.alpacon.io", paymentAPIStagingURL},
		{"staging dev2 region", "https://myws.dev2.alpacon.io", paymentAPIStagingURL},
		{"short hostname fallback", "https://alpacon.io", paymentAPIProdURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetPaymentAPIBaseURL(tt.workspaceURL)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetWorkspaceID(t *testing.T) {
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
	assert.NoError(t, err)
	assert.Equal(t, "uuid-2", id)
}

func TestGetWorkspaceID_NotFound(t *testing.T) {
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
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetUsageEstimate(t *testing.T) {
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
			"services":  map[string]any{},
			"metadata":  nil,
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	estimate, err := GetUsageEstimate(ac, ts.URL, "uuid-123")
	assert.NoError(t, err)
	assert.Equal(t, "KRW", estimate.Currency)
	assert.Equal(t, 30, estimate.BillingPeriod.TotalDays)
	assert.Equal(t, "Alpacon Core", estimate.Subscription.ProductName)
}

func TestGetUsageEstimate_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := GetUsageEstimate(ac, ts.URL, "uuid-123")
	assert.Error(t, err)
}
