package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

func TestGetAPITokenList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []APITokenResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, APITokenResponse{
					ID:   fmt.Sprintf("tid-%d", i),
					Name: fmt.Sprintf("token-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, APITokenResponse{
					ID:   fmt.Sprintf("tid-p2-%d", i),
					Name: fmt.Sprintf("token-p2-%d", i),
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[APITokenResponse]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	tokens, err := GetAPITokenList(ac)
	if err != nil {
		t.Fatalf("GetAPITokenList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(tokens) != 150 {
		t.Errorf("expected 150 tokens, got %d", len(tokens))
	}
}

func TestGetAPITokenIDByName(t *testing.T) {
	tests := []struct {
		name      string
		tokenName string
		count     int
		wantID    string
		wantErr   bool
	}{
		{"found", "ci-token", 1, "token-uuid-abc", false},
		{"not found", "ghost-token", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var results []APITokenResponse
				if tt.count > 0 {
					results = append(results, APITokenResponse{ID: tt.wantID, Name: tt.tokenName})
				}
				resp := api.ListResponse[APITokenResponse]{Count: tt.count, Results: results}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetAPITokenIDByName(ac, tt.tokenName)

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

func TestCreateAPIToken(t *testing.T) {
	const wantKey = "secret-api-key-xyz"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		resp := APITokenResponse{ID: "new-token-id", Name: "ci-token", Key: wantKey, UpdatedAt: time.Now()}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	key, err := CreateAPIToken(ac, APITokenRequest{Name: "ci-token"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != wantKey {
		t.Errorf("expected key %q, got %q", wantKey, key)
	}
}

func TestDeleteAPIToken(t *testing.T) {
	var deleteCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		deleteCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	if err := DeleteAPIToken(ac, "token-id-to-delete"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleteCalled {
		t.Error("DELETE request was not sent")
	}
}
