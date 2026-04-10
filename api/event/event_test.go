package event

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEventList_NoExtraPagination(t *testing.T) {
	var eventRequestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		count := eventRequestCount.Add(1)
		if count > 1 {
			t.Errorf("extra request detected: request #%d to %s (should be single request)", count, r.URL.String())
			return
		}

		pageSize := r.URL.Query().Get("page_size")
		if pageSize != "25" {
			t.Errorf("expected page_size=25, got %s", pageSize)
		}

		var results []EventDetails
		for i := range 25 {
			results = append(results, EventDetails{
				ID:          fmt.Sprintf("evt-%d", i),
				Server:      types.ServerSummary{Name: "test-server"},
				Shell:       "bash",
				Line:        fmt.Sprintf("cmd-%d", i),
				RequestedBy: types.UserSummary{Name: "admin"},
			})
		}

		resp := api.ListResponse[EventDetails]{
			Count:   200, // more items exist on server
			Results: results,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	events, err := GetEventList(ac, 25, "", "")
	if err != nil {
		t.Fatalf("GetEventList error: %v", err)
	}

	totalRequests := int(eventRequestCount.Load())
	if totalRequests != 1 {
		t.Errorf("expected 1 request, got %d", totalRequests)
	}
	if len(events) != 25 {
		t.Errorf("expected 25 events, got %d", len(events))
	}
}

func TestPollCommandExecution(t *testing.T) {
	tests := []struct {
		name           string
		statusSequence []string
		wantStatus     string
		wantResult     string
		wantRequests   int
	}{
		{
			name:           "running then completed",
			statusSequence: []string{"running", "running", "completed"},
			wantStatus:     "completed",
			wantResult:     "done",
			wantRequests:   3,
		},
		{
			name:           "acked then completed (backwards compat)",
			statusSequence: []string{"acked", "completed"},
			wantStatus:     "completed",
			wantResult:     "done",
			wantRequests:   2,
		},
		{
			name:           "immediate terminal status",
			statusSequence: []string{"error"},
			wantStatus:     "error",
			wantResult:     "done",
			wantRequests:   1,
		},
		{
			name:           "queued then delivered then running then success",
			statusSequence: []string{"queued", "delivered", "running", "success"},
			wantStatus:     "success",
			wantResult:     "done",
			wantRequests:   4,
		},
		{
			name:           "scheduled then queued then success",
			statusSequence: []string{"scheduled", "queued", "success"},
			wantStatus:     "success",
			wantResult:     "done",
			wantRequests:   3,
		},
		{
			name:           "verifying then running then success",
			statusSequence: []string{"verifying", "running", "success"},
			wantStatus:     "success",
			wantResult:     "done",
			wantRequests:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqCount atomic.Int32

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(reqCount.Add(1)) - 1
				if idx >= len(tt.statusSequence) {
					idx = len(tt.statusSequence) - 1
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(EventDetails{
					ID:     "cmd-1",
					Status: tt.statusSequence[idx],
					Result: "done",
				})
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{
				HTTPClient: ts.Client(),
				BaseURL:    ts.URL,
			}

			result, err := PollCommandExecution(ac, "cmd-1")
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.wantResult, result.Result)
			assert.Equal(t, tt.wantRequests, int(reqCount.Load()))
		})
	}
}
