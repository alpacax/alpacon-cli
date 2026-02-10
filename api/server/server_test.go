package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
)

func TestGetServerList_PaginationBug(t *testing.T) {
	var requestCount atomic.Int32

	// mock server: 150 servers total (page1=100, page2=50)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)

		// more than 3 requests means infinite loop
		if count > 3 {
			t.Fatalf("infinite loop detected: request #%d (page param: %s)", count, r.URL.Query().Get("page"))
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []ServerDetails
		switch page {
		case "1", "": // page=1 or unspecified
			for i := 0; i < 100; i++ {
				results = append(results, ServerDetails{
					ID:       fmt.Sprintf("id-%d", i),
					Name:     fmt.Sprintf("server-%d", i),
					RemoteIP: "10.0.0.1",
					Owner:    iam.UserSummary{Name: "admin"},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, ServerDetails{
					ID:       fmt.Sprintf("id-p2-%d", i),
					Name:     fmt.Sprintf("server-p2-%d", i),
					RemoteIP: "10.0.0.2",
					Owner:    iam.UserSummary{Name: "admin"},
				})
			}
		}

		resp := api.ListResponse[ServerDetails]{
			Count:   150,
			Current: 1,
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

	servers, err := GetServerList(ac)
	if err != nil {
		t.Fatalf("GetServerList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	t.Logf("total requests: %d", totalRequests)
	t.Logf("returned servers: %d", len(servers))

	// expected: page1(100) + page2(50) = 150 items, 2 requests
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d (pagination not working correctly)", totalRequests)
	}
	if len(servers) != 150 {
		t.Errorf("expected 150 servers, got %d", len(servers))
	}
}
