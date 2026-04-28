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
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []CommandAclResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, CommandAclResponse{
					ID:        fmt.Sprintf("acl-%d", i),
					TokenName: "my-token",
					Command:   fmt.Sprintf("cmd-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, CommandAclResponse{
					ID:        fmt.Sprintf("acl-p2-%d", i),
					TokenName: "my-token",
					Command:   fmt.Sprintf("cmd-p2-%d", i),
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
		_ = json.NewEncoder(w).Encode(resp)
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

func TestGetServerAclList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d", count)
			return
		}

		page := r.URL.Query().Get("page")
		var results []serverAclResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, serverAclResponse{
					ID:        fmt.Sprintf("sacl-%d", i),
					Token:     "token-id-1",
					TokenName: "my-token",
					Server:    serverAclServer{ID: fmt.Sprintf("srv-%d", i), Name: fmt.Sprintf("server-%d", i)},
				})
			}
		case "2":
			for i := 0; i < 30; i++ {
				results = append(results, serverAclResponse{
					ID:        fmt.Sprintf("sacl-p2-%d", i),
					Token:     "token-id-1",
					TokenName: "my-token",
					Server:    serverAclServer{ID: fmt.Sprintf("srv-p2-%d", i), Name: fmt.Sprintf("server-p2-%d", i)},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[serverAclResponse]{
			Count:   130,
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

	acls, err := GetServerAclList(ac, "token-id-1")
	if err != nil {
		t.Fatalf("GetServerAclList error: %v", err)
	}

	if int(requestCount.Load()) != 2 {
		t.Errorf("expected 2 requests, got %d", requestCount.Load())
	}
	if len(acls) != 130 {
		t.Errorf("expected 130 ACLs, got %d", len(acls))
	}
	if acls[0].ServerName != "server-0" {
		t.Errorf("expected server name 'server-0', got %q", acls[0].ServerName)
	}
}
