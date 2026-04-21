package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

// groupListResponse is a minimal helper type for mock IAM group responses.
type groupListItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestResolveGroupIDs_UUID_PassThrough(t *testing.T) {
	// UUIDs should pass through without any HTTP call.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	input := []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	}
	got, err := resolveGroupIDs(ac, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(got))
	}
	for i, id := range input {
		if got[i] != id {
			t.Errorf("index %d: expected %q, got %q", i, id, got[i])
		}
	}
}

func TestResolveGroupIDs_NameResolved(t *testing.T) {
	const wantID = "aaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ListResponse[groupListItem]{
			Count:   1,
			Results: []groupListItem{{ID: wantID, Name: "dev"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	got, err := resolveGroupIDs(ac, []string{"dev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 ID, got %d", len(got))
	}
	if got[0] != wantID {
		t.Errorf("expected %q, got %q", wantID, got[0])
	}
}

func TestResolveGroupIDs_NameNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ListResponse[groupListItem]{Count: 0, Results: []groupListItem{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := resolveGroupIDs(ac, []string{"nonexistent-group"})
	if err == nil {
		t.Fatal("expected error for unknown group name, got nil")
	}
}

func TestResolveGroupIDs_Empty(t *testing.T) {
	// Empty input should return empty slice without any HTTP call.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	got, err := resolveGroupIDs(ac, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d items", len(got))
	}
}
