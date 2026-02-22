package iam

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
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
		_ = json.NewEncoder(w).Encode(resp)
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
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
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
		_ = json.NewEncoder(w).Encode(resp)
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

func TestGetUserIDByName(t *testing.T) {
	tests := []struct {
		name     string
		username string
		count    int
		wantID   string
		wantErr  bool
	}{
		{"found", "alice", 1, "user-uuid-abc", false},
		{"not found", "nobody", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var results []UserResponse
				if tt.count > 0 {
					results = append(results, UserResponse{ID: tt.wantID, Username: tt.username})
				}
				resp := api.ListResponse[UserResponse]{Count: tt.count, Results: results}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetUserIDByName(ac, tt.username)

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

func TestGetGroupIDByName(t *testing.T) {
	tests := []struct {
		name      string
		groupName string
		count     int
		wantID    string
		wantErr   bool
	}{
		{"found", "admins", 1, "group-uuid-xyz", false},
		{"not found", "ghost-group", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var results []GroupResponse
				if tt.count > 0 {
					results = append(results, GroupResponse{ID: tt.wantID, Name: tt.groupName})
				}
				resp := api.ListResponse[GroupResponse]{Count: tt.count, Results: results}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetGroupIDByName(ac, tt.groupName)

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

func TestCreateUser_SetsIsActive(t *testing.T) {
	var gotIsActive bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req UserCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		gotIsActive = req.IsActive
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	// Pass IsActive=false intentionally; CreateUser must override it to true.
	err := CreateUser(ac, UserCreateRequest{Username: "newuser", IsActive: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotIsActive {
		t.Error("expected IsActive=true to be sent in request body, got false")
	}
}

func TestGetUserList_StatusMapping(t *testing.T) {
	users := []UserResponse{
		{ID: "1", Username: "superadmin", IsSuperuser: true},
		{ID: "2", Username: "staffuser", IsStaff: true},
		{ID: "3", Username: "activeuser", IsActive: true},
		{ID: "4", Username: "inactive"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ListResponse[UserResponse]{Count: len(users), Results: users}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	list, err := GetUserList(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("expected 4 users, got %d", len(list))
	}

	expected := []string{"superuser", "staff", "active", "inactive"}
	for i, u := range list {
		if u.Status != expected[i] {
			t.Errorf("user[%d] status: expected %q, got %q", i, expected[i], u.Status)
		}
	}
}

func TestAddMember(t *testing.T) {
	const (
		groupID = "group-uuid-111"
		userID  = "user-uuid-222"
	)
	var membershipPostCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/groups/"):
			resp := api.ListResponse[GroupResponse]{Count: 1, Results: []GroupResponse{{ID: groupID, Name: "admins"}}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/users/"):
			resp := api.ListResponse[UserResponse]{Count: 1, Results: []UserResponse{{ID: userID, Username: "alice"}}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/memberships/"):
			membershipPostCalled = true
			var req MemberAddRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Group != groupID {
				t.Errorf("expected group id %q, got %q", groupID, req.Group)
			}
			if req.User != userID {
				t.Errorf("expected user id %q, got %q", userID, req.User)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	err := AddMember(ac, MemberAddRequest{Group: "admins", User: "alice", Role: "member"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !membershipPostCalled {
		t.Error("membership POST was not called")
	}
}
