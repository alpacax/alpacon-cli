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

// The server caps page_size at 100 for both its page-number and cursor pagination.
const maxPageSize = 100

// cursorRestartLimit is how many extra walks FetchCursorPages may start.
const cursorRestartLimit = 2

// cursorRestartTick is the base gap before a restart: a refused point-in-time open
// is the cluster declining another reader, so the restart must not ask again at once.
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

// FetchAllCursorPages walks every cursor page. It is FetchCursorPages with no bound.
func FetchAllCursorPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string) ([]T, error) {
	return FetchCursorPages[T](ac, endpoint, params, math.MaxInt)
}

// FetchCursorPages follows the Elasticsearch cursor contract, accumulating up to limit items.
// A walk the server says is over is started again from the first page, up to
// cursorRestartLimit times.
//
// On failure the items collected so far come back alongside the error. They are one
// walk's items, never spliced across restarts. Only a nil error means the walk
// finished, so a caller that shows items it got with an error must say they are partial.
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

// isRestartableCursorError reports whether err is one a walk from the first page
// answers, since that walk mints a snapshot of its own and sends no cursor.
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
