package event

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// noSeqBound is the toSeq sentinel that leaves getCommandChunks unbounded (seq
// is 0-indexed, so 0 is a valid upper bound and cannot mean "no bound").
const noSeqBound = -1

// Chunk represents a single stdout/stderr chunk produced during command execution.
type Chunk struct {
	Seq     int    `json:"seq"`
	Content string `json:"content"`
}

// getCommandChunks fetches chunks for cmdID with seq in [fromSeq, toSeq], sorted by seq
// ascending even when the server ignores the ordering param. A negative toSeq omits the
// upper bound.
func getCommandChunks(ac *client.AlpaconClient, cmdID api.PathID, fromSeq, toSeq int) ([]Chunk, error) {
	endpoint := api.IDURL(getEventURL, cmdID, "chunks", nil)
	params := map[string]string{
		"seq__gte": strconv.Itoa(fromSeq),
		"ordering": "seq",
	}
	// Omit seq__lte when unbounded: the server's integer filter rejects an empty value.
	if toSeq >= 0 {
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
// order. It returns an empty string when no chunks were produced.
func GetCommandOutput(ac *client.AlpaconClient, cmdID string) (string, error) {
	pathID, err := api.RequireID(cmdID, "command")
	if err != nil {
		return "", err
	}
	return getCommandOutputByPathID(ac, pathID)
}

func getCommandOutputByPathID(ac *client.AlpaconClient, pathID api.PathID) (string, error) {
	chunks, err := getCommandChunks(ac, pathID, 0, noSeqBound)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Content)
	}
	return b.String(), nil
}

// ResolveCommandOutput reconstructs a finished command's output from its chunks,
// falling back to the result field the command detail carries.
func ResolveCommandOutput(ac *client.AlpaconClient, cmdID, result string) (string, error) {
	chunked, err := GetCommandOutput(ac, cmdID)
	return pickCommandOutput(result, chunked, err)
}

func resolveCommandOutputByPathID(ac *client.AlpaconClient, pathID api.PathID, result string) (string, error) {
	chunked, err := getCommandOutputByPathID(ac, pathID)
	return pickCommandOutput(result, chunked, err)
}

// pickCommandOutput prefers the chunked output. A chunk fetch failure is fatal only when
// result is empty and the status is not 404: a server predating the chunk endpoint answers
// the legacy field alone, while an empty result leaves nothing to print, and reporting success
// would describe a command that produced no output. A 404 says the server has no chunk store,
// so its empty result is the whole output.
func pickCommandOutput(result, chunked string, chunkErr error) (string, error) {
	if chunkErr != nil {
		if result != "" || utils.HTTPStatusCode(chunkErr) == http.StatusNotFound {
			return result, nil
		}
		return "", chunkErr
	}
	if chunked != "" {
		return chunked, nil
	}
	return result, nil
}
