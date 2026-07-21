package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// copyParams returns a shallow copy so the pagination loop never mutates the caller's map.
func copyParams(params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+2)
	maps.Copy(out, params)
	return out
}

func FetchAllPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string) ([]T, error) {
	var result []T
	page := 1
	const pageSize = 100

	params = copyParams(params)
	params["page"] = strconv.Itoa(page)
	params["page_size"] = strconv.Itoa(pageSize)

	for {
		var response ListResponse[T]
		responseBody, err := ac.SendGetRequest(utils.BuildURL(endpoint, "", params))
		if err != nil {
			return nil, err
		}

		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, err
		}

		result = append(result, response.Results...)

		if response.Next == 0 {
			break
		}
		page++
		params["page"] = strconv.Itoa(page)
	}

	return result, nil
}

// FetchCursorPages follows the Elasticsearch cursor contract, accumulating up to limit items.
func FetchCursorPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string, limit int) ([]T, error) {
	if limit <= 0 {
		return nil, nil
	}

	// The server caps page_size at 100 (ESCursorPagination.max_page_size).
	const maxPageSize = 100

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
