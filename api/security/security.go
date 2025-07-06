package security

import (
	"encoding/json"
	"path"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	baseURL       = "/api/security/"
	commandAclURL = "command-acl/"
)

func GetCommandAclList(ac *client.AlpaconClient, tokenId string) ([]CommandAclResponse, error) {
	var response api.ListResponse[CommandAclResponse]
	var result []CommandAclResponse

	params := map[string]string{
		"token": tokenId,
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(baseURL, commandAclURL, params))
	if err != nil {
		return result, err
	}

	if err = json.Unmarshal(responseBody, &response); err != nil {
		return result, err
	}

	for _, commandAcl := range response.Results {
		result = append(result, CommandAclResponse{
			Id:        commandAcl.Id,
			Token:     commandAcl.Token,
			TokenName: commandAcl.TokenName,
			Command:   commandAcl.Command,
		})
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
