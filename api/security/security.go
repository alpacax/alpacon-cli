package security

import (
	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	commandAclURL = "/api/security/command-acl/"
)

func GetCommandAclList(ac *client.AlpaconClient, tokenId string) ([]CommandAclResponse, error) {
	params := map[string]string{
		"token": tokenId,
	}
	return api.FetchAllPages[CommandAclResponse](ac, commandAclURL, params)
}

func AddCommandAcl(ac *client.AlpaconClient, request CommandAclRequest) error {
	_, err := ac.SendPostRequest(commandAclURL, request)
	if err != nil {
		return err
	}

	return nil
}

func DeleteCommandAcl(ac *client.AlpaconClient, commandAclId string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(commandAclURL, commandAclId, nil))
	if err != nil {
		return err
	}

	return nil
}
