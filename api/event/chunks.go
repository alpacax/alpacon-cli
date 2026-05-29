package event

import (
	"sort"
	"strconv"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

// Chunk represents a single stdout/stderr chunk produced during command execution.
type Chunk struct {
	Seq     int    `json:"seq"`
	Content string `json:"content"`
}

// GetCommandChunks fetches all chunks for cmdID whose seq is >= fromSeq.
// Pagination is handled transparently by api.FetchAllPages. Results are sorted
// by seq ascending: the streaming consumers (warm-fire, gap-fill, drain) rely
// on this invariant, so we request server-side ordering and also sort
// defensively in case the server does not honor it (or paginates unordered).
func GetCommandChunks(ac *client.AlpaconClient, cmdID string, fromSeq int) ([]Chunk, error) {
	endpoint := "/api/events/commands/" + cmdID + "/chunks/"
	params := map[string]string{
		"seq__gte": strconv.Itoa(fromSeq),
		"ordering": "seq",
	}
	chunks, err := api.FetchAllPages[Chunk](ac, endpoint, params)
	if err != nil {
		return nil, err
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Seq < chunks[j].Seq })
	return chunks, nil
}
