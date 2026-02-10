package security

import (
	"encoding/json"
	"path"
	"strconv"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	baseURL       = "/api/security/"
	commandAclURL = "command-acl/"
)

func GetCommandAclList(ac *client.AlpaconClient, tokenId string) ([]CommandAclResponse, error) {
	var result []CommandAclResponse
	page := 1
	const pageSize = 100

	params := map[string]string{
		"token":     tokenId,
		"page":      strconv.Itoa(page),
		"page_size": strconv.Itoa(pageSize),
	}
	for {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(baseURL, commandAclURL, params))
		if err != nil {
			return result, err
		}

		var response api.ListResponse[CommandAclResponse]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return result, err
		}

		result = append(result, response.Results...)

		if len(response.Results) < pageSize {
			break
		}
		page++
		params["page"] = strconv.Itoa(page)
	}

	return result, nil
}

func AddCommandAcl(ac *client.AlpaconClient, request CommandAclRequest) error {
	_, err := ac.SendPostRequest(utils.BuildURL(baseURL, commandAclURL, nil), request)
	if err != nil {
		return err
	}

	return nil
}

func DeleteCommandAcl(ac *client.AlpaconClient, commandAclId string) error {
	relativePath := path.Join(commandAclURL, commandAclId)
	_, err := ac.SendDeleteRequest(utils.BuildURL(baseURL, relativePath, nil))
	if err != nil {
		return err
	}

	return nil
}
