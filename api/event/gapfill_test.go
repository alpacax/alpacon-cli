package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

// holeServer serves /chunks/?seq__gte=N returning every seq >= N except those
// in `hole`. fetches counts the chunk requests. Used to simulate a seq that is
// never persisted server-side.
func holeServer(t *testing.T, maxSeq int, hole map[int]bool, fetches *int) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*fetches++
		from, _ := strconv.Atoi(r.URL.Query().Get("seq__gte"))
		var results []Chunk
		for s := from; s <= maxSeq; s++ {
			if hole[s] {
				continue
			}
			results = append(results, Chunk{Seq: s, Content: fmt.Sprintf("c%d\n", s)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

func swapGapFillNow(fn func() time.Time) func() {
	old := gapFillNow
	gapFillNow = fn
	return func() { gapFillNow = old }
}

func swapGapFillVars(initial time.Duration, factor int, max time.Duration, maxNoProgress int) func() {
	oi, of, om, on := gapFillInitialInterval, gapFillBackoffFactor, gapFillMaxInterval, gapFillMaxNoProgress
	gapFillInitialInterval, gapFillBackoffFactor, gapFillMaxInterval, gapFillMaxNoProgress = initial, factor, max, maxNoProgress
	return func() {
		gapFillInitialInterval, gapFillBackoffFactor, gapFillMaxInterval, gapFillMaxNoProgress = oi, of, om, on
	}
}

// While lastSeq is stuck, gapped chunks arriving within the backoff window must
// not each trigger a REST fetch.
func TestApplyChunk_ThrottlesRefetchWithinBackoffWindow(t *testing.T) {
	var fetches int
	ac := holeServer(t, 20, map[int]bool{1: true}, &fetches)

	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0 // seq 0 already applied
	// Clock frozen: after the first (immediate) attempt, every further gapped
	// chunk is inside the window and must be skipped without a fetch.
	for seq := 2; seq <= 10; seq++ {
		lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: seq, Content: fmt.Sprintf("c%d\n", seq)}, out, g)
	}

	assert.Equal(t, 1, fetches, "only the first gapped chunk should fetch; rest throttled")
	assert.Equal(t, 0, lastSeq, "lastSeq stays behind the permanent hole")
}

// After gapFillMaxNoProgress no-progress attempts, the missing seq is skipped:
// present chunks are printed, the hole is recorded in skipped, and lastSeq
// advances so live streaming resumes.
func TestApplyChunk_GivesUpAndSkipsPermanentGap(t *testing.T) {
	var fetches int
	ac := holeServer(t, 20, map[int]bool{1: true}, &fetches)

	defer swapGapFillVars(1*time.Millisecond, 2, 2*time.Millisecond, 3)()
	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0
	for seq := 2; seq <= 6; seq++ {
		now = now.Add(10 * time.Millisecond) // advance past the window each time
		lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: seq, Content: fmt.Sprintf("c%d\n", seq)}, out, g)
	}

	assert.LessOrEqual(t, fetches, 3, "fetches bounded by gapFillMaxNoProgress")
	assert.Equal(t, []int{1}, g.skipped, "seq 1 recorded as skipped")
	assert.Equal(t, 6, lastSeq, "streaming resumes past the skipped hole")
	assert.Equal(t, "c2\nc3\nc4\nc5\nc6\n", out.String(), "c1 lost, everything else printed in order")
}

// A gap that heals before the give-up limit resets the backoff and prints
// everything, with no seq recorded as skipped.
func TestApplyChunk_ResetsBackoffWhenGapHeals(t *testing.T) {
	var fetches int
	// No hole: the gap-fill fetch immediately returns the missing seq.
	ac := holeServer(t, 20, map[int]bool{}, &fetches)

	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0
	// seq 3 opens a gap over 1,2 which REST fills at once.
	lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: 3, Content: "c3\n"}, out, g)

	assert.Equal(t, 3, lastSeq)
	assert.Equal(t, 0, g.noProgress, "progress resets the no-progress counter")
	assert.Empty(t, g.skipped)
	assert.Equal(t, "c1\nc2\nc3\n", out.String())
}

// Skipped seqs that got persisted late are recovered and printed at command
// end (out of order); no output is written for seqs still missing.
func TestRecoverSkippedChunks_RecoversLatePersistedSeq(t *testing.T) {
	var fetches int
	// seq 1 now exists (arrived late); seq 4 is still a permanent hole.
	ac := holeServer(t, 10, map[int]bool{4: true}, &fetches)

	g := &gapFillState{skipped: []int{1, 4}}
	out := &bytes.Buffer{}
	recoverSkippedChunks(ac, "cmd", g, out)

	assert.Equal(t, "c1\n", out.String(), "recovered seq 1 printed; seq 4 stays missing")
}

// No skipped seqs -> no fetch, no output.
func TestRecoverSkippedChunks_NoopWhenNothingSkipped(t *testing.T) {
	var fetches int
	ac := holeServer(t, 10, map[int]bool{}, &fetches)

	g := &gapFillState{}
	out := &bytes.Buffer{}
	recoverSkippedChunks(ac, "cmd", g, out)

	assert.Equal(t, 0, fetches, "no skipped seqs means no recovery fetch")
	assert.Empty(t, out.String())
}
