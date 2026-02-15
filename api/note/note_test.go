package note

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
)

func TestGetNoteList_NoExtraPagination(t *testing.T) {
	var noteRequestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// server name lookup by ID (for each note)
		if strings.HasPrefix(r.URL.Path, "/api/servers/servers/srv-") {
			resp := server.ServerDetails{ID: "srv-1", Name: "test-server"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// user name lookup by ID (for each note)
		if strings.HasPrefix(r.URL.Path, "/api/iam/users/usr-") {
			resp := iam.UserDetailAttributes{Username: "test-user"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// note list endpoint
		if strings.HasPrefix(r.URL.Path, "/api/servers/notes") {
			count := noteRequestCount.Add(1)
			if count > 1 {
				t.Errorf("extra note request detected: request #%d (should be single request)", count)
				return
			}

			pageSize := r.URL.Query().Get("page_size")
			if pageSize != "25" {
				t.Errorf("expected page_size=25, got %s", pageSize)
			}

			results := []NoteDetails{
				{ID: "note-1", Server: "srv-1", Author: "usr-1", Content: "hello", Private: false},
				{ID: "note-2", Server: "srv-1", Author: "usr-1", Content: "world", Private: true},
			}

			resp := api.ListResponse[NoteDetails]{
				Count:   200, // more items exist on server
				Results: results,
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		t.Errorf("unexpected request: %s", r.URL.String())
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	notes, err := GetNoteList(ac, "", 25)
	if err != nil {
		t.Fatalf("GetNoteList error: %v", err)
	}

	totalRequests := int(noteRequestCount.Load())
	if totalRequests != 1 {
		t.Errorf("expected 1 note request, got %d", totalRequests)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes, got %d", len(notes))
	}

	if len(notes) > 0 && notes[0].Server != "test-server" {
		t.Errorf("expected server name 'test-server', got '%s'", notes[0].Server)
	}
	if len(notes) > 0 && notes[0].Author != "test-user" {
		t.Errorf("expected author 'test-user', got '%s'", notes[0].Author)
	}
}
