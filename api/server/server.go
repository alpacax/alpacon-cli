package server

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	serverURL            = "/api/servers/servers/"
	registrationTokenURL = "/api/servers/registration-tokens/"
	registrationGuideURL = "/api/servers/registration-methods/token-install/guide/"
)

// ErrRegistrationTokenNotFound is returned when no registration token matches the given name.
var ErrRegistrationTokenNotFound = errors.New("no registration token found with the given name")

func GetServerList(ac *client.AlpaconClient) ([]ServerAttributes, error) {
	servers, err := api.FetchAllPages[ServerDetails](ac, serverURL, nil)
	if err != nil {
		return nil, err
	}

	var serverList []ServerAttributes
	for _, server := range servers {
		serverList = append(serverList, ServerAttributes{
			Name:      server.Name,
			IP:        server.RemoteIP,
			OS:        fmt.Sprintf("%s %s", server.OSName, server.OSVersion),
			Connected: server.IsConnected,
			Owner:     server.Owner.Name,
		})
	}

	return serverList, nil
}

func GetServerDetail(ac *client.AlpaconClient, serverName string) ([]byte, error) {
	serverID, err := GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, err
	}

	body, err := ac.SendGetRequest(utils.BuildURL(serverURL, serverID, nil))
	if err != nil {
		return nil, err
	}

	return body, nil
}

func DeleteServer(ac *client.AlpaconClient, serverName string) error {
	serverID, err := GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(utils.BuildURL(serverURL, serverID, nil))
	if err != nil {
		return err
	}

	return nil
}

func GetServerIDByName(ac *client.AlpaconClient, serverName string) (string, error) {
	params := map[string]string{
		"name": serverName,
	}
	body, err := ac.SendGetRequest(utils.BuildURL(serverURL, "", params))
	if err != nil {
		return "", err
	}

	var response api.ListResponse[ServerDetails]
	err = json.Unmarshal(body, &response)
	if err != nil {
		return "", err
	}

	if response.Count == 0 {
		return "", errors.New("no server found with the given name")
	}

	return response.Results[0].ID, nil
}

func GetServerNameByID(ac *client.AlpaconClient, serverID string) (string, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(serverURL, serverID, nil))
	if err != nil {
		return "", err
	}

	var response ServerDetails
	err = json.Unmarshal(body, &response)
	if err != nil {
		return "", err
	}

	return response.Name, nil
}

func UpdateServer(ac *client.AlpaconClient, serverName string) ([]byte, error) {
	serverID, err := GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, err
	}

	responseBody, err := ac.SendGetRequest(utils.BuildURL(serverURL, serverID, nil))
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(utils.BuildURL(serverURL, serverID, nil), data)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func CreateRegistrationToken(ac *client.AlpaconClient, req RegistrationTokenRequest) (RegistrationTokenCreatedResponse, error) {
	var response RegistrationTokenCreatedResponse
	responseBody, err := ac.SendPostRequest(registrationTokenURL, req)
	if err != nil {
		return RegistrationTokenCreatedResponse{}, err
	}

	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return RegistrationTokenCreatedResponse{}, err
	}

	return response, nil
}

func GetRegistrationTokenByName(ac *client.AlpaconClient, name string) (RegistrationTokenDetails, error) {
	params := map[string]string{"search": name}
	tokens, err := api.FetchAllPages[RegistrationTokenDetails](ac, registrationTokenURL, params)
	if err != nil {
		return RegistrationTokenDetails{}, err
	}

	for _, t := range tokens {
		if t.Name == name {
			return t, nil
		}
	}

	return RegistrationTokenDetails{}, ErrRegistrationTokenNotFound
}

func DeleteRegistrationToken(ac *client.AlpaconClient, tokenID string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(registrationTokenURL, tokenID, nil))
	return err
}

func RegenerateRegistrationToken(ac *client.AlpaconClient, name string) (RegistrationTokenCreatedResponse, error) {
	existing, err := GetRegistrationTokenByName(ac, name)
	if err != nil {
		return RegistrationTokenCreatedResponse{}, err
	}

	created, err := CreateRegistrationToken(ac, RegistrationTokenRequest{
		Name:          existing.Name,
		AllowedGroups: existing.AllowedGroups,
	})
	if err != nil {
		return RegistrationTokenCreatedResponse{}, err
	}

	if err = DeleteRegistrationToken(ac, existing.ID); err != nil {
		if rollbackErr := DeleteRegistrationToken(ac, created.ID); rollbackErr != nil {
			return RegistrationTokenCreatedResponse{}, fmt.Errorf("delete old registration token: %w; rollback new registration token: %v", err, rollbackErr)
		}
		return RegistrationTokenCreatedResponse{}, fmt.Errorf("delete old registration token: %w", err)
	}

	return created, nil
}

func ListRegistrationTokens(ac *client.AlpaconClient) ([]RegistrationTokenDetails, error) {
	return api.FetchAllPages[RegistrationTokenDetails](ac, registrationTokenURL, nil)
}

func GetRegistrationGuide(ac *client.AlpaconClient, platform, serverName, tokenID string) (string, error) {
	req := RegistrationMethodGuideRequest{
		Platform:          platform,
		ServerName:        serverName,
		RegistrationToken: tokenID,
	}

	body, err := ac.SendPostRequest(registrationGuideURL, req)
	if err != nil {
		return "", err
	}

	var response RegistrationMethodGuideResponse
	if err = json.Unmarshal(body, &response); err != nil {
		return "", err
	}

	return response.Content, nil
}
