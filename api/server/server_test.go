package server

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
)

func TestGetServerList_PaginationBug(t *testing.T) {
	var requestCount atomic.Int32

	// mock server: 150 servers total (page1=100, page2=50)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)

		// more than 3 requests means infinite loop
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page param: %s)", count, r.URL.Query().Get("page"))
			return
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
					Owner:    types.UserSummary{Name: "admin"},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, ServerDetails{
					ID:       fmt.Sprintf("id-p2-%d", i),
					Name:     fmt.Sprintf("server-p2-%d", i),
					RemoteIP: "10.0.0.2",
					Owner:    types.UserSummary{Name: "admin"},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[ServerDetails]{
			Count:   150,
			Current: 1,
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

func TestGetServerIDByName(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		count      int
		wantID     string
		wantErr    bool
	}{
		{"found", "my-server", 1, "server-uuid-abc", false},
		{"not found", "missing-server", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var results []ServerDetails
				if tt.count > 0 {
					results = append(results, ServerDetails{ID: tt.wantID, Name: tt.serverName})
				}
				resp := api.ListResponse[ServerDetails]{Count: tt.count, Results: results}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			id, err := GetServerIDByName(ac, tt.serverName)

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

func TestGetServerNameByID(t *testing.T) {
	const wantName = "prod-server"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ServerDetails{ID: "some-id", Name: wantName}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	name, err := GetServerNameByID(ac, "some-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != wantName {
		t.Errorf("expected name %q, got %q", wantName, name)
	}
}

func TestCreateRegistrationToken(t *testing.T) {
	want := RegistrationTokenCreatedResponse{
		ID:   "token-uuid-abc",
		Name: "new-server",
		Key:  "alpacax_sometoken",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	got, err := CreateRegistrationToken(ac, RegistrationTokenRequest{Name: "new-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Key != want.Key {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestListRegistrationTokens(t *testing.T) {
	tokens := []RegistrationTokenDetails{
		{ID: "uuid-1", Name: "prod-token", Enabled: true},
		{ID: "uuid-2", Name: "dev-token", Enabled: true},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		resp := api.ListResponse[RegistrationTokenDetails]{Count: 2, Results: tokens}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	got, err := ListRegistrationTokens(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(got))
	}
	if got[0].Name != "prod-token" || got[1].Name != "dev-token" {
		t.Errorf("unexpected token names: %v", got)
	}
}

func TestRegenerateRegistrationToken(t *testing.T) {
	const (
		oldID   = "old-token-uuid"
		newID   = "new-token-uuid"
		newKey  = "alpacax_newkey"
		tokName = "prod-token"
	)

	var createCalled, deleteCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "search="):
			// GetRegistrationTokenByName
			resp := api.ListResponse[RegistrationTokenDetails]{
				Count:   1,
				Results: []RegistrationTokenDetails{{ID: oldID, Name: tokName}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodPost:
			// CreateRegistrationToken — must happen before delete
			if deleteCalled {
				t.Error("CreateRegistrationToken called after DeleteRegistrationToken")
			}
			createCalled = true
			resp := RegistrationTokenCreatedResponse{ID: newID, Name: tokName, Key: newKey}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodDelete:
			// DeleteRegistrationToken
			if !createCalled {
				t.Error("DeleteRegistrationToken called before CreateRegistrationToken")
			}
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	got, err := RegenerateRegistrationToken(ac, tokName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !createCalled {
		t.Error("CreateRegistrationToken was not called")
	}
	if !deleteCalled {
		t.Error("DeleteRegistrationToken was not called")
	}
	if got.ID != newID || got.Key != newKey {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestDeleteServer(t *testing.T) {
	const serverID = "delete-server-id"
	var deleteCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "name=") {
			// GetServerIDByName
			resp := api.ListResponse[ServerDetails]{
				Count:   1,
				Results: []ServerDetails{{ID: serverID, Name: "target-server"}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if r.Method == http.MethodDelete {
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	if err := DeleteServer(ac, "target-server"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleteCalled {
		t.Error("DELETE request was not sent")
	}
}
