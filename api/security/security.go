package security

import (
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
	params := map[string]string{
		"token": tokenId,
	}
	return api.FetchAllPages[CommandAclResponse](ac, baseURL+commandAclURL, params)
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
