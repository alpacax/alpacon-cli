package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestCreateRegistrationToken_WithExpiresAt(t *testing.T) {
	expiresAt := "2026-12-31T00:00:00Z"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req RegistrationTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if req.ExpiresAt == nil || *req.ExpiresAt != expiresAt {
			t.Errorf("expected expires_at %q, got %v", expiresAt, req.ExpiresAt)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RegistrationTokenCreatedResponse{
			ID:        "tok-uuid",
			Name:      "x",
			Key:       "alpacax_key",
			ExpiresAt: &expiresAt,
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := CreateRegistrationToken(ac, RegistrationTokenRequest{Name: "x", ExpiresAt: &expiresAt})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRegistrationToken_WithoutExpiresAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "expires_at") {
			t.Errorf("expected no expires_at in body, got: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RegistrationTokenCreatedResponse{ID: "tok-uuid", Name: "x", Key: "alpacax_key"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := CreateRegistrationToken(ac, RegistrationTokenRequest{Name: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteRegistrationToken_ByName_Success(t *testing.T) {
	const tokenID = "tok-uuid-abc"
	var deleteCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "search=") {
			resp := api.ListResponse[RegistrationTokenDetails]{
				Count:   1,
				Results: []RegistrationTokenDetails{{ID: tokenID, Name: "target-token", Enabled: true}},
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
	if err := DeleteRegistrationToken(ac, "target-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleteCalled {
		t.Error("DELETE request was not sent")
	}
}

func TestDeleteRegistrationToken_ByName_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ListResponse[RegistrationTokenDetails]{Count: 0, Results: []RegistrationTokenDetails{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	err := DeleteRegistrationToken(ac, "ghost")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrRegistrationTokenNotFound) {
		t.Errorf("expected ErrRegistrationTokenNotFound, got %v", err)
	}
}

func TestGetRegistrationTokenAttributes_MapsGroupNames(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/iam/groups/") {
			type groupItem struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			resp := api.ListResponse[groupItem]{
				Count: 2,
				Results: []groupItem{
					{ID: "uuid-g1", Name: "dev"},
					{ID: "uuid-g2", Name: "ops"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// registration tokens
		resp := api.ListResponse[RegistrationTokenDetails]{
			Count: 1,
			Results: []RegistrationTokenDetails{
				{ID: "tok-1", Name: "my-token", AllowedGroups: []string{"uuid-g1", "uuid-g2"}, Enabled: true},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	attrs, err := GetRegistrationTokenAttributes(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attrs) != 1 {
		t.Fatalf("expected 1 attribute, got %d", len(attrs))
	}
	if attrs[0].AllowedGroups != "dev, ops" {
		t.Errorf("expected AllowedGroups %q, got %q", "dev, ops", attrs[0].AllowedGroups)
	}
}

func TestGetRegistrationTokenAttributes_FallsBackToUUID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/iam/groups/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := api.ListResponse[RegistrationTokenDetails]{
			Count: 1,
			Results: []RegistrationTokenDetails{
				{ID: "tok-1", Name: "my-token", AllowedGroups: []string{"uuid-g1"}, Enabled: true},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	attrs, err := GetRegistrationTokenAttributes(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attrs) != 1 {
		t.Fatalf("expected 1 attribute, got %d", len(attrs))
	}
	if attrs[0].AllowedGroups != "uuid-g1" {
		t.Errorf("expected AllowedGroups %q, got %q", "uuid-g1", attrs[0].AllowedGroups)
	}
}

func TestGetRegistrationTokenAttributes_ExpiresNever(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/iam/groups/") {
			resp := api.ListResponse[struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}]{Count: 0}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		resp := api.ListResponse[RegistrationTokenDetails]{
			Count: 1,
			Results: []RegistrationTokenDetails{
				{ID: "tok-1", Name: "no-expire-token", AllowedGroups: nil, ExpiresAt: nil, Enabled: true},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	attrs, err := GetRegistrationTokenAttributes(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attrs) != 1 {
		t.Fatalf("expected 1 attribute, got %d", len(attrs))
	}
	if attrs[0].ExpiresAt != "never" {
		t.Errorf("expected ExpiresAt %q, got %q", "never", attrs[0].ExpiresAt)
	}
}

func TestGetRegistrationTokenAttributes_EmptyList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/iam/groups/") {
			resp := api.ListResponse[struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}]{Count: 0}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		resp := api.ListResponse[RegistrationTokenDetails]{Count: 0, Results: []RegistrationTokenDetails{}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	attrs, err := GetRegistrationTokenAttributes(ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attrs) != 0 {
		t.Errorf("expected empty slice, got %d items", len(attrs))
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
