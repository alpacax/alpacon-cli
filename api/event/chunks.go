package event

import (
	"sort"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

// Chunk represents a single stdout/stderr chunk produced during command execution.
type Chunk struct {
	Seq     int    `json:"seq"`
	Content string `json:"content"`
}

// GetCommandChunks fetches chunks for cmdID with seq >= fromSeq, sorted by seq
// ascending. The streaming consumers rely on that order, so we sort defensively
// in case the server does not honor the ordering param.
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

// GetCommandOutput reconstructs the full command output from its chunks in seq
// order. Empty when no chunks were produced. Used by non-streaming paths (exec
// logs, polling fallback) where Result is empty under the chunk contract.
func GetCommandOutput(ac *client.AlpaconClient, cmdID string) (string, error) {
	chunks, err := GetCommandChunks(ac, cmdID, 0)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Content)
	}
	return b.String(), nil
}
