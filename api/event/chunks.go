package event

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

// noSeqBound is the toSeq sentinel that leaves getCommandChunks unbounded (seq
// is 0-indexed, so 0 is a valid upper bound and cannot mean "no bound").
const noSeqBound = -1

// Chunk represents a single stdout/stderr chunk produced during command execution.
type Chunk struct {
	Seq     int    `json:"seq"`
	Content string `json:"content"`
}

// getCommandChunks fetches chunks for cmdID with seq in [fromSeq, toSeq],
// sorted by seq ascending. A negative toSeq (noSeqBound) omits the upper
// bound. The streaming consumers rely on the order, so we sort defensively
// in case the server does not honor the ordering param.
func getCommandChunks(ac *client.AlpaconClient, cmdID string, fromSeq, toSeq int) ([]Chunk, error) {
	endpoint := "/api/events/commands/" + url.PathEscape(cmdID) + "/chunks/"
	params := map[string]string{
		"seq__gte": strconv.Itoa(fromSeq),
		"ordering": "seq",
	}
	if toSeq >= 0 {
		// Omit rather than send empty when unbounded: the server's strict
		// integer filter rejects an empty seq__lte.
		params["seq__lte"] = strconv.Itoa(toSeq)
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
	chunks, err := getCommandChunks(ac, cmdID, 0, noSeqBound)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Content)
	}
	return b.String(), nil
}
