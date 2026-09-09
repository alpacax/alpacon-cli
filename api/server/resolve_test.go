package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeServerName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, want string
		wantErr           bool
	}{
		{"empty", "", "", true}, {"blank", " \t\n", "", true},
		{"padded", " \tserver\n", "server", false}, {"internal space", "my server", "my server", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeServerName(tc.input)
			if tc.wantErr {
				require.EqualError(t, err, "server name is required")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveServerNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		names, want, requests []string
		wantErr               bool
	}{
		{name: "empty", want: []string{}},
		{name: "blank entries", names: []string{" ", "\t"}, want: []string{}},
		{name: "trim and drop", names: []string{" a ", "", " \t", "b"}, want: []string{"id-a", "id-b"}, requests: []string{"a", "b"}},
		{name: "duplicates", names: []string{"b", "a", "b"}, want: []string{"id-b", "id-a", "id-b"}, requests: []string{"b", "a", "b"}},
		{name: "fail fast", names: []string{"a", "missing", "b"}, requests: []string{"a", "missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := r.URL.Query().Get("name")
				mu.Lock()
				requests = append(requests, name)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if name == "missing" {
					_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []map[string]string{{"id": "id-" + name}}})
			}))
			defer ts.Close()
			got, err := ResolveServerNames(&client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client()}, tc.names)
			if tc.wantErr {
				require.ErrorContains(t, err, `server "missing" not found`)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tc.requests, requests)
		})
	}
}

func TestGetServerIDByNameRejectsBlankBeforeRequest(t *testing.T) {
	t.Parallel()
	_, err := GetServerIDByName(nil, " \t")
	require.EqualError(t, err, "server name is required")
}
