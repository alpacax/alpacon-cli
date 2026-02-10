package iam

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

func TestGetUserList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Fatalf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []UserResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, UserResponse{
					ID:       fmt.Sprintf("uid-%d", i),
					Username: fmt.Sprintf("user-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, UserResponse{
					ID:       fmt.Sprintf("uid-p2-%d", i),
					Username: fmt.Sprintf("user-p2-%d", i),
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[UserResponse]{
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

	users, err := GetUserList(ac)
	if err != nil {
		t.Fatalf("GetUserList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(users) != 150 {
		t.Errorf("expected 150 users, got %d", len(users))
	}
}

func TestGetGroupList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Fatalf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []GroupResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, GroupResponse{
					ID:   fmt.Sprintf("gid-%d", i),
					Name: fmt.Sprintf("group-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, GroupResponse{
					ID:   fmt.Sprintf("gid-p2-%d", i),
					Name: fmt.Sprintf("group-p2-%d", i),
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[GroupResponse]{
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

	groups, err := GetGroupList(ac)
	if err != nil {
		t.Fatalf("GetGroupList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(groups) != 150 {
		t.Errorf("expected 150 groups, got %d", len(groups))
	}
}
