package log

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
)

func TestGetSystemLogList_NoExtraPagination(t *testing.T) {
	var logRequestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// server ID lookup
		if strings.HasPrefix(r.URL.Path, "/api/servers/servers") {
			resp := api.ListResponse[server.ServerDetails]{
				Count:   1,
				Results: []server.ServerDetails{{ID: "srv-1", Name: "test-server"}},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// log endpoint
		if strings.HasPrefix(r.URL.Path, "/api/history/logs") {
			count := logRequestCount.Add(1)
			if count > 1 {
				t.Fatalf("extra log request detected: request #%d (should be single request)", count)
			}

			pageSize := r.URL.Query().Get("page_size")
			if pageSize != "25" {
				t.Errorf("expected page_size=25, got %s", pageSize)
			}

			var results []LogEntry
			for i := 0; i < 25; i++ {
				results = append(results, LogEntry{
					Program: "sshd",
					Level:   20,
					Process: "main",
					Msg:     fmt.Sprintf("log-%d", i),
				})
			}

			resp := api.ListResponse[LogEntry]{
				Count:   200, // more items exist on server
				Results: results,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		t.Fatalf("unexpected request: %s", r.URL.String())
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	logs, err := GetSystemLogList(ac, "test-server", 25)
	if err != nil {
		t.Fatalf("GetSystemLogList error: %v", err)
	}

	totalRequests := int(logRequestCount.Load())
	if totalRequests != 1 {
		t.Errorf("expected 1 log request, got %d", totalRequests)
	}
	if len(logs) != 25 {
		t.Errorf("expected 25 logs, got %d", len(logs))
	}
}
