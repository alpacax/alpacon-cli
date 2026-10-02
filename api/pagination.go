package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// The server caps page_size at 100 for both paginators
// (api.pagination.MyPageNumberPagination and history.pagination.ESCursorPagination).
const maxPageSize = 100

// cursorRestartLimit is how many extra walks FetchCursorPages may start after the
// server says the chain it was following is finished. A bound and not a retry loop
// forever: every one of those answers is also what a fresh walk can run into, so an
// unbounded restart would keep a command alive against a cluster that is refusing it.
const cursorRestartLimit = 2

// cursorRestartTick is the base gap before a restart, doubled per attempt and
// overridden by a server-sent Retry-After, both through utils.NextPollBackoff. A
// refused point-in-time open is the cluster declining to take another reader, so the
// restart answering it has to pause rather than ask again at once. A test sets it to
// 0; nothing else writes it.
var cursorRestartTick = time.Second

// copyParams returns a shallow copy so the pagination loop never mutates the caller's map.
func copyParams(params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+2)
	maps.Copy(out, params)
	return out
}

// FetchAllPages walks every page. It is FetchPagesUpTo with no bound.
func FetchAllPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string) ([]T, error) {
	return FetchPagesUpTo[T](ac, endpoint, params, math.MaxInt)
}

// FetchPagesUpTo walks PageNumber pages until it has limit items, so a caller asking for
// more than one page's worth is not silently cut off at the server's page cap.
func FetchPagesUpTo[T any](ac *client.AlpaconClient, endpoint string, params map[string]string, limit int) ([]T, error) {
	if limit <= 0 {
		return nil, nil
	}

	params = copyParams(params)
	// A PageNumber offset is (page-1)*page_size, so page_size has to stay fixed for the whole
	// walk. Shrinking it on the last request would move that page back over an earlier offset.
	params["page_size"] = strconv.Itoa(min(maxPageSize, limit))

	result := make([]T, 0, min(limit, maxPageSize))
	for page := 1; len(result) < limit; page++ {
		params["page"] = strconv.Itoa(page)

		responseBody, err := ac.SendGetRequest(utils.BuildURL(endpoint, "", params))
		if err != nil {
			return nil, fmt.Errorf("fetching page %d from %s: %w", page, endpoint, err)
		}

		var response ListResponse[T]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, fmt.Errorf("decoding page %d from %s: %w", page, endpoint, err)
		}

		result = append(result, response.Results...)
		if response.Next == 0 || len(response.Results) == 0 {
			break
		}
	}

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// FetchCursorPages follows the Elasticsearch cursor contract, accumulating up to limit items.
//
// A cursor carries the id of the point-in-time snapshot its page was read from, and a
// snapshot that is gone cannot be resumed—the only way on is a fresh walk from the
// first page. So the three codes the server sends for a chain it will not continue
// (see utils.APICursorExpired and its neighbours) start the walk again instead of
// ending it, cursorRestartLimit times.
//
// Once those run out, and on any other failure, the items collected so far come back
// alongside the error instead of being dropped: a partly read audit trail still
// answers most of what was asked. They are one walk's items, never spliced across
// restarts, so a short result is still a consistent snapshot rather than a mix of two.
// A nil error is the whole of what says the walk finished, so a caller that shows a
// result it got with an error has to say that it is partial.
func FetchCursorPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string, limit int) ([]T, error) {
	if limit <= 0 {
		return nil, nil
	}

	var longest []T
	for attempt := 0; ; attempt++ {
		items, err := walkCursorPages[T](ac, endpoint, params, limit)
		if err == nil {
			return items, nil
		}
		// The longest walk, not the latest: a restart that dies on its own first page
		// must not cost the pages the attempt before it had already read.
		if len(items) > len(longest) {
			longest = items
		}
		if attempt >= cursorRestartLimit || !isRestartableCursorError(err) {
			return longest, err
		}
		time.Sleep(utils.NextPollBackoff(cursorRestartTick, attempt, utils.RetryAfter(err)))
	}
}

// isRestartableCursorError reports whether err is the server saying the walk is over
// rather than that the request was wrong: a snapshot that expired, a cursor it will
// not take, or a search it would not start. A walk from the first page answers all
// three, since it mints a snapshot of its own and sends no cursor.
func isRestartableCursorError(err error) bool {
	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case utils.APICursorExpired, utils.APIInvalidCursor, utils.APISearchUnavailable:
		return true
	default:
		return false
	}
}

// walkCursorPages follows one cursor chain from its first page, returning the items it
// reached before any failure so its caller can decide between restarting and keeping them.
func walkCursorPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string, limit int) ([]T, error) {
	params = copyParams(params)
	// Drop any caller-supplied cursor so the first request starts from the first page.
	// A restart relies on this too: the copy is per walk, so the cursor the previous
	// one left behind cannot follow it.
	delete(params, "cursor")

	result := make([]T, 0, min(limit, maxPageSize))
	cursor := ""
	for len(result) < limit {
		params["page_size"] = strconv.Itoa(min(maxPageSize, limit-len(result)))
		if cursor != "" {
			params["cursor"] = cursor
		}

		responseBody, err := ac.SendGetRequest(utils.BuildURL(endpoint, "", params))
		if err != nil {
			return result, fmt.Errorf("fetching cursor page from %s: %w", endpoint, err)
		}

		var page CursorListResponse[T]
		if err = json.Unmarshal(responseBody, &page); err != nil {
			return result, fmt.Errorf("decoding cursor page from %s: %w", endpoint, err)
		}

		result = append(result, page.Results...)
		if page.Next == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Next
	}

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
