package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

func TestGetAPITokenList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Fatalf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
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
		json.NewEncoder(w).Encode(resp)
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
