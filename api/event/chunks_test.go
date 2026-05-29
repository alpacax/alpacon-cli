package event

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCommandChunks_PassesSeqGteAndReturnsResults(t *testing.T) {
	cmdID := "a1b2c3d4-1234-5678-abcd-000000000000"
	var capturedQuery string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.True(t, strings.HasPrefix(r.URL.Path, fmt.Sprintf("/api/events/commands/%s/chunks/", cmdID)),
			"unexpected path: %s", r.URL.Path)
		capturedQuery = r.URL.RawQuery

		resp := api.ListResponse[Chunk]{
			Count: 2,
			Results: []Chunk{
				{Seq: 5, Content: "hello\n"},
				{Seq: 6, Content: "world\n"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	got, err := GetCommandChunks(ac, cmdID, 5)
	require.NoError(t, err)
	assert.Equal(t, []Chunk{
		{Seq: 5, Content: "hello\n"},
		{Seq: 6, Content: "world\n"},
	}, got)
	assert.Contains(t, capturedQuery, "seq__gte=5")
	assert.Contains(t, capturedQuery, "ordering=seq")
}

// TestGetCommandChunks_SortsBySeq verifies out-of-order server results are
// returned sorted by seq.
func TestGetCommandChunks_SortsBySeq(t *testing.T) {
	cmdID := "a1b2c3d4-1234-5678-abcd-000000000000"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ListResponse[Chunk]{
			Count: 3,
			Results: []Chunk{
				{Seq: 2, Content: "c\n"},
				{Seq: 0, Content: "a\n"},
				{Seq: 1, Content: "b\n"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	got, err := GetCommandChunks(ac, cmdID, 0)
	require.NoError(t, err)
	assert.Equal(t, []Chunk{
		{Seq: 0, Content: "a\n"},
		{Seq: 1, Content: "b\n"},
		{Seq: 2, Content: "c\n"},
	}, got)
}
