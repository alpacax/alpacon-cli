package security

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

func TestGetCommandAclList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Fatalf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []CommandAclResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, CommandAclResponse{
					Id:      fmt.Sprintf("acl-%d", i),
					Token:   "token-id-1",
					Command: fmt.Sprintf("cmd-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, CommandAclResponse{
					Id:      fmt.Sprintf("acl-p2-%d", i),
					Token:   "token-id-1",
					Command: fmt.Sprintf("cmd-p2-%d", i),
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[CommandAclResponse]{
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

	acls, err := GetCommandAclList(ac, "token-id-1")
	if err != nil {
		t.Fatalf("GetCommandAclList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(acls) != 150 {
		t.Errorf("expected 150 ACLs, got %d", len(acls))
	}
}
