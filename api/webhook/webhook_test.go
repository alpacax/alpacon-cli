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

// TestGetWebhookIDByName puts the decoy first, so a dropped or wrong-key filter answers with it.
func TestGetWebhookIDByName(t *testing.T) {
	t.Parallel()
	webhooks := []WebhookResponse{
		{ID: "id-first", Name: "first-webhook"},
		{ID: "wh-uuid-abc", Name: "alert-webhook"},
	}

	tests := []struct {
		name        string
		webhookName string
		wantID      string
		wantErr     bool
		wantCall    bool
	}{
		{"found", "alert-webhook", "wh-uuid-abc", false, true},
		{"padded name", "  alert-webhook  ", "wh-uuid-abc", false, true},
		{"not found", "ghost-webhook", "", true, true},
		{"whitespace only name", "   ", "", true, false},
		{"empty name", "", "", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var called atomic.Bool

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				filter := strings.TrimSpace(r.URL.Query().Get("name"))

				results := webhooks
				if filter != "" {
					results = nil
					for _, wh := range webhooks {
						if wh.Name == filter {
							results = append(results, wh)
						}
					}
				}

				resp := api.ListResponse[WebhookResponse]{Count: len(results), Results: results}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetWebhookIDByName(ac, tt.webhookName)

			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, id)
				if tt.webhookName == "" {
					require.ErrorIs(t, err, api.ErrBlankName)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, id)
			}
			assert.Equal(t, tt.wantCall, called.Load(), "whether the name reached the API")
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
