package api

import (
	"encoding/json"
	"strconv"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

func FetchAllPages[T any](ac *client.AlpaconClient, endpoint string, params map[string]string) ([]T, error) {
	var result []T
	page := 1
	const pageSize = 100

	if params == nil {
		params = make(map[string]string)
	}
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
