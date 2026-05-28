package event

import (
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
// Pagination is handled transparently by api.FetchAllPages.
func GetCommandChunks(ac *client.AlpaconClient, cmdID string, fromSeq int) ([]Chunk, error) {
	endpoint := "/api/events/commands/" + cmdID + "/chunks/"
	params := map[string]string{
		"seq__gte": strconv.Itoa(fromSeq),
	}
	return api.FetchAllPages[Chunk](ac, endpoint, params)
}
