package security

import (
	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	commandAclURL       = "/api/security/command-acl/"
	serverAclURL        = "/api/security/server-acl/"
	serverAclBulkURL    = "/api/security/server-acl/bulk/"
	serverAclBulkDelURL = "/api/security/server-acl/bulk/delete/"
	fileAclURL          = "/api/security/file-acl/"
)

func GetCommandAclList(ac *client.AlpaconClient, tokenId string) ([]CommandAclResponse, error) {
	params := map[string]string{
		"token": tokenId,
	}
	return api.FetchAllPages[CommandAclResponse](ac, commandAclURL, params)
}

func AddCommandAcl(ac *client.AlpaconClient, request CommandAclRequest) error {
	_, err := ac.SendPostRequest(commandAclURL, request)
	return err
}

func DeleteCommandAcl(ac *client.AlpaconClient, commandAclId string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(commandAclURL, commandAclId, nil))
	return err
}

func GetServerAclList(ac *client.AlpaconClient, tokenID string) ([]ServerAclAttributes, error) {
	params := map[string]string{"token": tokenID}
	raw, err := api.FetchAllPages[serverAclResponse](ac, serverAclURL, params)
	if err != nil {
		return nil, err
	}
	out := make([]ServerAclAttributes, len(raw))
	for i, r := range raw {
		out[i] = ServerAclAttributes{
			ID:         r.ID,
			TokenName:  r.TokenName,
			ServerName: r.Server.Name,
		}
	}
	return out, nil
}

func AddServerAcl(ac *client.AlpaconClient, request ServerAclRequest) error {
	_, err := ac.SendPostRequest(serverAclURL, request)
	return err
}

func BulkAddServerAcl(ac *client.AlpaconClient, request ServerAclBulkRequest) error {
	_, err := ac.SendPostRequest(serverAclBulkURL, request)
	return err
}

func BulkDeleteServerAcl(ac *client.AlpaconClient, request ServerAclBulkRequest) error {
	_, err := ac.SendPostRequest(serverAclBulkDelURL, request)
	return err
}

func DeleteServerAcl(ac *client.AlpaconClient, serverAclID string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(serverAclURL, serverAclID, nil))
	return err
}

func GetFileAclList(ac *client.AlpaconClient, tokenID string) ([]FileAclResponse, error) {
	params := map[string]string{"token": tokenID}
	return api.FetchAllPages[FileAclResponse](ac, fileAclURL, params)
}

func AddFileAcl(ac *client.AlpaconClient, request FileAclRequest) error {
	_, err := ac.SendPostRequest(fileAclURL, request)
	return err
}

func DeleteFileAcl(ac *client.AlpaconClient, fileAclID string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(fileAclURL, fileAclID, nil))
	return err
}
