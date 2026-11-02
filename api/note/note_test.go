package note

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetNoteList_SendsFilterOrderingAndPinnedParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		serverName      string
		pinnedOnly      bool
		serverFound     bool
		wantErr         bool
		wantErrContains string
		wantParams      map[string]string
		absentParams    []string
	}{
		{
			name:         "no server name skips resolution and sends no server param",
			wantParams:   map[string]string{"ordering": "-added_at", "page_size": "25", "page": "1"},
			absentParams: []string{"server", "serverID", "pinned"},
		},
		{
			name:        "resolves server name to the server param",
			serverName:  "my-server",
			serverFound: true,
			wantParams:  map[string]string{"server": "srv-uuid", "ordering": "-added_at"},
			// serverID is the name the server never filtered on.
			absentParams: []string{"serverID"},
		},
		{
			name:       "pinned only adds the pinned param",
			pinnedOnly: true,
			wantParams: map[string]string{"pinned": "true", "ordering": "-added_at"},
		},
		{
			name:            "unknown server name returns an error",
			serverName:      "ghost",
			serverFound:     false,
			wantErr:         true,
			wantErrContains: "no server found with the given name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var captured url.Values

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/servers/servers/":
					if tt.serverFound {
						_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-uuid"}]}`))
					} else {
						_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
					}
				case "/api/servers/notes/":
					mu.Lock()
					captured = r.URL.Query()
					mu.Unlock()
					_, _ = w.Write([]byte(`{"count":0,"next":0,"results":[]}`))
				default:
					t.Errorf("unexpected request path: %s", r.URL.Path)
					http.Error(w, "unexpected request path", http.StatusNotFound)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			_, err := GetNoteList(ac, tt.serverName, 25, tt.pinnedOnly)

			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContains)
				return
			}
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			for key, want := range tt.wantParams {
				assert.Equal(t, want, captured.Get(key))
			}
			for _, key := range tt.absentParams {
				assert.NotContains(t, captured, key)
			}
		})
	}
}

// api.TestFetchPagesUpTo_* pins the page walk; this pins that tail reaches it as the limit, so a
// server offering endless pages still yields exactly tail notes.
func TestGetNoteList_PassesTailAsTheLimit(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		size, sizeErr := strconv.Atoi(r.URL.Query().Get("page_size"))
		page, pageErr := strconv.Atoi(r.URL.Query().Get("page"))
		if sizeErr != nil || pageErr != nil {
			t.Errorf("page and page_size must be integers, got page=%q page_size=%q",
				r.URL.Query().Get("page"), r.URL.Query().Get("page_size"))
			http.Error(w, "bad pagination query", http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests++
		mu.Unlock()

		results := make([]NoteResponse, size)
		_ = json.NewEncoder(w).Encode(api.ListResponse[NoteResponse]{Count: 1000, Next: page + 1, Results: results})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	notes, err := GetNoteList(ac, "", 250, false)

	require.NoError(t, err)
	assert.Len(t, notes, 250)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 3, requests)
}

func TestGetNoteList_MapsResponseFields(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := []NoteResponse{{
			ID:      "note-1",
			Server:  types.ServerSummary{ID: "srv-1", Name: "test-server"},
			Author:  types.UserSummary{Name: "test-user"},
			Content: "hello",
			Private: true,
			Pinned:  true,
			AddedAt: time.Now().Add(-3 * time.Hour),
		}}
		_ = json.NewEncoder(w).Encode(api.ListResponse[NoteResponse]{Count: 1, Next: 0, Results: results})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	notes, err := GetNoteList(ac, "", 25, false)

	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, NoteDetails{
		ID:      "note-1",
		Server:  "test-server",
		Author:  "test-user",
		Content: "hello",
		Private: true,
		Pinned:  true,
		AddedAt: "3 hours ago",
	}, notes[0])
}

func TestNoteCallsRefuseAPathTraversingIDBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	const badID = "../../etc/test"
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
	}{
		{"GetNoteDetail", func(ac *client.AlpaconClient) error {
			_, err := GetNoteDetail(ac, badID)
			return err
		}},
		{"UpdateNote", func(ac *client.AlpaconClient) error {
			_, err := UpdateNote(ac, badID)
			return err
		}},
		{"DeleteNote", func(ac *client.AlpaconClient) error { return DeleteNote(ac, badID) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := tt.call(ac)

			require.ErrorIs(t, err, utils.ErrInvalidID)
		})
	}
}

// UpdateNote opens an editor, so it is not driven here.
func TestNoteCallsTrimAValidIDIntoOnePathSegment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
		want string
	}{
		{"GetNoteDetail", func(ac *client.AlpaconClient) error {
			_, err := GetNoteDetail(ac, " 42 ")
			return err
		}, "GET /api/servers/notes/42/"},
		{"DeleteNote", func(ac *client.AlpaconClient) error { return DeleteNote(ac, " 42 ") }, "DELETE /api/servers/notes/42/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec atomic.Value
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.Store(r.Method + " " + r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(ts.Close)
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := tt.call(ac)

			require.NoError(t, err)
			assert.Equal(t, tt.want, rec.Load())
		})
	}
}
