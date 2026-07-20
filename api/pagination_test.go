package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cursorItem struct {
	Name string `json:"name"`
}

func TestFetchCursorPages_SinglePageNullNext(t *testing.T) {
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		// Raw wire form of ESCursorPagination: null next, extra fields ignored.
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[{"name":"a"},{"name":"b"}]}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 100)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, 1, requests)
}

func TestFetchCursorPages_FollowsCursorAndCaps(t *testing.T) {
	var gotCursors []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCursors = append(gotCursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}, {Name: "b"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
			Results: []cursorItem{{Name: "c"}, {Name: "d"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{"", "TOKEN2"}, gotCursors)
	require.Len(t, items, 3)
	assert.Equal(t, "c", items[2].Name)
}

func TestFetchCursorPages_PageSize(t *testing.T) {
	tests := []struct {
		limit        int
		wantPageSize string
	}{
		{250, "100"},
		{3, "3"},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.limit), func(t *testing.T) {
			var gotPageSize string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if gotPageSize == "" {
					gotPageSize = r.URL.Query().Get("page_size")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Results: []cursorItem{{Name: "a"}}})
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			_, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, tt.limit)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPageSize, gotPageSize)
		})
	}
}

func TestFetchCursorPages_NonPositiveLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request expected for non-positive limit")
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 0)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestFetchCursorPages_StopsOnEmptyResults(t *testing.T) {
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		// Pathological page: next token but no results must not loop forever.
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Next: "MORE"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Equal(t, 1, requests)
}

func TestFetchCursorPages_IgnoresStaleCursorParam(t *testing.T) {
	var gotCursors []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCursors = append(gotCursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Results: []cursorItem{{Name: "a"}}})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	// A caller-supplied cursor must not leak into the first request.
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", map[string]string{"cursor": "STALE"}, 10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, []string{""}, gotCursors)
}
