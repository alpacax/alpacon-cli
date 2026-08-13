package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// The server caps page_size at 100 for both paginators
// (api.pagination.MyPageNumberPagination and history.pagination.ESCursorPagination).
const maxPageSize = 100

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
func FetchCursorPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string, limit int) ([]T, error) {
	if limit <= 0 {
		return nil, nil
	}

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
			return nil, fmt.Errorf("fetching cursor page from %s: %w", endpoint, err)
		}

		var page CursorListResponse[T]
		if err = json.Unmarshal(responseBody, &page); err != nil {
			return nil, fmt.Errorf("decoding cursor page from %s: %w", endpoint, err)
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
