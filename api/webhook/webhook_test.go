package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWebhookList_Pagination(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		var results []WebhookResponse
		switch page {
		case "1", "":
			for i := range 100 {
				results = append(results, WebhookResponse{
					ID:    fmt.Sprintf("wh-id-%d", i),
					Name:  fmt.Sprintf("webhook-%d", i),
					Owner: types.UserSummary{Name: "admin"},
				})
			}
		case "2":
			for i := range 40 {
				results = append(results, WebhookResponse{
					ID:    fmt.Sprintf("wh-p2-%d", i),
					Name:  fmt.Sprintf("webhook-p2-%d", i),
					Owner: types.UserSummary{Name: "admin"},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[WebhookResponse]{Count: 140, Next: next, Results: results}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	webhooks, err := GetWebhookList(ac)
	if err != nil {
		t.Fatalf("GetWebhookList error: %v", err)
	}
	if int(requestCount.Load()) != 2 {
		t.Errorf("expected 2 requests, got %d", requestCount.Load())
	}
	if len(webhooks) != 140 {
		t.Errorf("expected 140 webhooks, got %d", len(webhooks))
	}
}

func TestGetWebhookIDByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		webhookName string
		count       int
		wantID      string
		wantErr     bool
	}{
		{"found", "alert-webhook", 1, "wh-uuid-abc", false},
		{"not found", "ghost-webhook", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var results []WebhookResponse
				if tt.count > 0 {
					results = append(results, WebhookResponse{ID: tt.wantID, Name: tt.webhookName})
				}
				resp := api.ListResponse[WebhookResponse]{Count: tt.count, Results: results}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetWebhookIDByName(ac, tt.webhookName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if id != tt.wantID {
					t.Errorf("expected id %q, got %q", tt.wantID, id)
				}
			}
		})
	}
}

func TestDeleteWebhook(t *testing.T) {
	t.Parallel()
	const webhookID = "wh-delete-uuid"
	var deleteCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "name="):
			resp := api.ListResponse[WebhookResponse]{
				Count:   1,
				Results: []WebhookResponse{{ID: webhookID, Name: "target-webhook"}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodDelete:
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	if err := DeleteWebhook(ac, "target-webhook"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleteCalled {
		t.Error("DELETE request was not sent")
	}
}

// TestGetWebhookIDByName_BlankNameResolvesNothing pins that a blank name never reaches
// the API, which drops the filter and would answer with the whole webhook list.
func TestGetWebhookIDByName_BlankNameResolvesNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		webhookName string
		wantID      string
		wantCall    bool
	}{
		{"exact name", "deploy-hook", "hook-uuid-abc", true},
		{"padded name", "  deploy-hook  ", "hook-uuid-abc", true},
		{"whitespace only name", "   ", "", false},
		{"empty name", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var called atomic.Bool

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				results := []WebhookResponse{{ID: "hook-uuid-abc", Name: "deploy-hook"}}
				if filter := strings.TrimSpace(r.URL.Query().Get("name")); filter != "" && filter != "deploy-hook" {
					results = nil
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[WebhookResponse]{Count: len(results), Results: results})
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetWebhookIDByName(ac, tt.webhookName)

			if tt.wantID == "" {
				require.Error(t, err)
				assert.Empty(t, id)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, id)
			}
			assert.Equal(t, tt.wantCall, called.Load(), "whether the name reached the API")
		})
	}
}
