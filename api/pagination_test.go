package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cursorItem struct {
	Name string `json:"name"`
}

type pageItem struct {
	ID string `json:"id"`
}

// requestRecorder records every request's query. httptest serves each request on its own
// goroutine, so the lock is what lets a test read the record back under go test -race.
type requestRecorder struct {
	mu       sync.Mutex
	requests []url.Values
}

func (rec *requestRecorder) record(r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	rec.requests = append(rec.requests, r.URL.Query())
}

func (rec *requestRecorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	return len(rec.requests)
}

func (rec *requestRecorder) queried(key string) []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	values := make([]string, 0, len(rec.requests))
	for _, q := range rec.requests {
		values = append(values, q.Get(key))
	}
	return values
}

func TestFetchCursorPages_SinglePageNullNext(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		// Raw wire form of ESCursorPagination: null next, extra fields ignored.
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[{"name":"a"},{"name":"b"}]}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 100)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, 1, rec.count())
}

func TestFetchCursorPages_FollowsCursorAndCaps(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}, {Name: "b"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
			Results: []cursorItem{{Name: "c"}, {Name: "d"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 3)
	require.NoError(t, err)
	assert.Equal(t, []string{"", "TOKEN2"}, rec.queried("cursor"))
	require.Len(t, items, 3)
	assert.Equal(t, "c", items[2].Name)
}

func TestFetchCursorPages_PageSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		limit        int
		wantPageSize string
	}{
		{250, "100"},
		{3, "3"},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.limit), func(t *testing.T) {
			rec := &requestRecorder{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Results: []cursorItem{{Name: "a"}}})
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			_, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, tt.limit)
			require.NoError(t, err)
			assert.Equal(t, []string{tt.wantPageSize}, rec.queried("page_size"))
		})
	}
}

func TestFetchCursorPages_NonPositiveLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{0, -1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("no request expected for non-positive limit")
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, limit)
			require.NoError(t, err)
			assert.Empty(t, items)
		})
	}
}

// servedNames is what a cursor walk yielded, in order, for comparing against the
// pages a server handed out.
func servedNames(items []cursorItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

// noRestartDelay drops the pause FetchCursorPages takes before a restart, so a test
// of the restart itself does not pay for it. The caller must be serial—
// cursorRestartTick is package state, and restoring it under a parallel neighbour
// that reads it would race.
func noRestartDelay(t *testing.T) {
	t.Helper()
	previous := cursorRestartTick
	cursorRestartTick = 0
	t.Cleanup(func() { cursorRestartTick = previous })
}

// newRestartingCursorServer serves two-page cursor walks and fails exactly one
// request: the failPage-th request it sees, with status and code. Everything after
// that is served normally, so a walk started again gets through.
func newRestartingCursorServer(t *testing.T, rec *requestRecorder, failPage, status int, code string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		if rec.count() == failPage {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"code":%q}`, code)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Results: []cursorItem{{Name: "b"}}})
	}))
}

// Serial: noRestartDelay writes package state.
func TestFetchCursorPages_RestartsABrokenWalk(t *testing.T) {
	noRestartDelay(t)
	tests := []struct {
		name        string
		failPage    int
		status      int
		code        string
		wantCursors []string
	}{
		{
			name:     "an expired snapshot mid walk",
			failPage: 2,
			status:   http.StatusBadRequest,
			code:     utils.APICursorExpired,
			// The restarted walk sends no cursor on its own first request.
			wantCursors: []string{"", "TOKEN2", "", "TOKEN2"},
		},
		{
			name:        "a cursor the server will not take",
			failPage:    2,
			status:      http.StatusBadRequest,
			code:        utils.APIInvalidCursor,
			wantCursors: []string{"", "TOKEN2", "", "TOKEN2"},
		},
		{
			// A refused point-in-time open can only land on a cursor-less request,
			// which is the one page a restart repeats rather than resumes.
			name:        "a search the cluster would not start",
			failPage:    1,
			status:      http.StatusServiceUnavailable,
			code:        utils.APISearchUnavailable,
			wantCursors: []string{"", "", "TOKEN2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &requestRecorder{}
			ts := newRestartingCursorServer(t, rec, tt.failPage, tt.status, tt.code)
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)

			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, servedNames(items))
			assert.Equal(t, tt.wantCursors, rec.queried("cursor"))
		})
	}
}

// Serial: noRestartDelay writes package state.
func TestFetchCursorPages_BoundsRestarts(t *testing.T) {
	noRestartDelay(t)
	rec := &requestRecorder{}
	// Every cursor-borne page loses its snapshot, so no walk ever finishes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}},
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"code":%q}`, utils.APICursorExpired)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)

	require.Error(t, err)
	// One walk plus cursorRestartLimit restarts, two requests each.
	assert.Equal(t, 2*(cursorRestartLimit+1), rec.count())
	assert.Equal(t, []string{"a"}, servedNames(items))
}

// Serial: noRestartDelay writes package state.
func TestFetchCursorPages_KeepsTheLongestWalkAcrossRestarts(t *testing.T) {
	noRestartDelay(t)
	rec := &requestRecorder{}
	// The first walk reads two pages before losing its snapshot; every walk after it
	// loses one on its own first page, so the most any restart can offer is nothing.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch rec.count() {
		case 1:
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN3",
				Results: []cursorItem{{Name: "b"}},
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"code":%q}`, utils.APICursorExpired)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)

	require.Error(t, err)
	// The pages the first walk read, not the empty result the last restart ended on.
	assert.Equal(t, []string{"a", "b"}, servedNames(items))
	// Three requests for the first walk, then one dead first page per restart.
	assert.Equal(t, 3+cursorRestartLimit, rec.count())
}

// Serial: the subject is a timing window, and noRestartDelay's seam is what it writes.
func TestFetchCursorPages_PausesBeforeARestart(t *testing.T) {
	noRestartDelay(t)
	const tick = 50 * time.Millisecond
	cursorRestartTick = tick

	rec := &requestRecorder{}
	ts := newRestartingCursorServer(t, rec, 1, http.StatusServiceUnavailable, utils.APISearchUnavailable)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	started := time.Now()
	_, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)
	elapsed := time.Since(started)

	require.NoError(t, err)
	// A cluster that just declined a reader must not be asked again in the same
	// millisecond, so the restart waits at least one tick.
	assert.GreaterOrEqual(t, elapsed, tick)
}

func TestFetchCursorPages_KeepsPagesReadBeforeAFailure(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	// A 500 is not the server refusing a cursor, so the walk ends rather than restarts—
	// and what it had already read comes back with the error instead of being dropped.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}},
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)

	require.Error(t, err)
	require.ErrorContains(t, err, "fetching cursor page from /api/history/logs/")
	assert.Equal(t, []string{"a"}, servedNames(items))
	assert.Equal(t, 2, rec.count())
}

func TestFetchCursorPages_MalformedJSON(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": [`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)
	require.Error(t, err)
	assert.Nil(t, items)
}

func TestFetchCursorPages_StopsOnEmptyResults(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		// Pathological page: next token but no results must not loop forever.
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Next: "MORE"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", nil, 10)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Equal(t, 1, rec.count())
}

func TestFetchCursorPages_IgnoresStaleCursorParam(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Results: []cursorItem{{Name: "a"}}})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	// A caller-supplied cursor must not leak into the first request.
	items, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", map[string]string{"cursor": "STALE"}, 10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, []string{""}, rec.queried("cursor"))
}

func TestFetchCursorPages_DoesNotMutateCallerParams(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{
				Next:    "TOKEN2",
				Results: []cursorItem{{Name: "a"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(CursorListResponse[cursorItem]{Results: []cursorItem{{Name: "b"}}})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	params := map[string]string{"app": "cert"}
	_, err := FetchCursorPages[cursorItem](ac, "/api/history/logs/", params, 10)
	require.NoError(t, err)
	// The helper must leave no page_size/cursor residue in the caller's map.
	assert.Equal(t, map[string]string{"app": "cert"}, params)
}

// newPageServer slices its items the way Django's PageNumberPagination does—offset
// (page-1)*page_size, taken from the request itself—so a page_size that changes mid-walk
// shows up as duplicated and skipped items instead of passing silently.
func newPageServer(t *testing.T, total int, rec *requestRecorder) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size, sizeErr := strconv.Atoi(r.URL.Query().Get("page_size"))
		page, pageErr := strconv.Atoi(r.URL.Query().Get("page"))
		if sizeErr != nil || pageErr != nil {
			t.Errorf("page and page_size must be integers, got page=%q page_size=%q",
				r.URL.Query().Get("page"), r.URL.Query().Get("page_size"))
			http.Error(w, "bad pagination query", http.StatusBadRequest)
			return
		}

		rec.record(r)

		offset := (page - 1) * size
		n := max(0, min(size, total-offset))
		results := make([]pageItem, 0, n)
		for i := range n {
			results = append(results, pageItem{ID: strconv.Itoa(offset + i)})
		}
		next := 0
		if offset+n < total {
			next = page + 1
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListResponse[pageItem]{Count: total, Next: next, Results: results})
	}))
}

// sequentialIDs is what newPageServer must yield end to end: every item once, in order.
func sequentialIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := range n {
		ids = append(ids, strconv.Itoa(i))
	}
	return ids
}

func servedIDs(items []pageItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestFetchPagesUpTo_WalksPagesWithoutGapsOrDuplicates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		total         int
		limit         int
		wantPageSizes []string
		wantPages     []string
		wantLen       int
	}{
		{
			name:          "limit under one page takes one request",
			total:         500,
			limit:         25,
			wantPageSizes: []string{"25"},
			wantPages:     []string{"1"},
			wantLen:       25,
		},
		{
			name:          "limit over one page keeps page_size fixed at the cap",
			total:         500,
			limit:         250,
			wantPageSizes: []string{"100", "100", "100"},
			wantPages:     []string{"1", "2", "3"},
			wantLen:       250,
		},
		{
			name:          "stops when the server runs out before the limit",
			total:         120,
			limit:         250,
			wantPageSizes: []string{"100", "100"},
			wantPages:     []string{"1", "2"},
			wantLen:       120,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &requestRecorder{}
			ts := newPageServer(t, tt.total, rec)
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			items, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", nil, tt.limit)

			require.NoError(t, err)
			assert.Equal(t, sequentialIDs(tt.wantLen), servedIDs(items))
			assert.Equal(t, tt.wantPageSizes, rec.queried("page_size"))
			assert.Equal(t, tt.wantPages, rec.queried("page"))
		})
	}
}

func TestFetchPagesUpTo_TruncatesWhenServerOverServes(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":5,"next":0,"results":[{"id":"0"},{"id":"1"},{"id":"2"},{"id":"3"},{"id":"4"}]}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	items, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", nil, 3)

	require.NoError(t, err)
	// Truncation keeps the first limit items, not the last: the walk starts at the newest page.
	assert.Equal(t, sequentialIDs(3), servedIDs(items))
}

func TestFetchPagesUpTo_SecondPageErrorDiscardsPartial(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"count":200,"next":2,"results":[{"id":"0"}]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	items, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", nil, 10)

	require.Error(t, err)
	// Which page of which endpoint failed is what makes a multi-page walk diagnosable.
	require.ErrorContains(t, err, "fetching page 2 from /api/servers/notes/")
	assert.Nil(t, items)
}

func TestFetchPagesUpTo_MalformedJSON(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": [`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	items, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", nil, 10)

	require.Error(t, err)
	require.ErrorContains(t, err, "decoding page 1 from /api/servers/notes/")
	assert.Nil(t, items)
}

func TestFetchPagesUpTo_DoesNotMutateCallerParams(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := newPageServer(t, 10, rec)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	params := map[string]string{"server": "srv-uuid"}

	_, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", params, 10)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"server": "srv-uuid"}, params)
}

func TestFetchPagesUpTo_NonPositiveLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{0, -1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request for limit %d: %s", limit, r.URL.String())
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			items, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", nil, limit)

			require.NoError(t, err)
			assert.Empty(t, items)
		})
	}
}

// A server that keeps claiming a next page while returning nothing would spin the loop forever.
func TestFetchPagesUpTo_StopsOnEmptyResults(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":500,"next":2,"results":[]}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	items, err := FetchPagesUpTo[pageItem](ac, "/api/servers/notes/", nil, 100)

	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Equal(t, []string{"1"}, rec.queried("page"))
}

func TestFetchAllPages_WalksEveryPageAtTheCap(t *testing.T) {
	t.Parallel()
	rec := &requestRecorder{}
	ts := newPageServer(t, 250, rec)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	items, err := FetchAllPages[pageItem](ac, "/api/servers/notes/", nil)

	require.NoError(t, err)
	assert.Equal(t, sequentialIDs(250), servedIDs(items))
	assert.Equal(t, []string{"100", "100", "100"}, rec.queried("page_size"))
	assert.Equal(t, []string{"1", "2", "3"}, rec.queried("page"))
}
