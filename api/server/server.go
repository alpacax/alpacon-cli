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
	serverURL = "/api/servers/servers/"
)

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

func CreateServer(ac *client.AlpaconClient, serverRequest ServerRequest) (ServerCreatedResponse, error) {
	var response ServerCreatedResponse
	responseBody, err := ac.SendPostRequest(serverURL, serverRequest)
	if err != nil {
		return ServerCreatedResponse{}, err
	}

	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return ServerCreatedResponse{}, err
	}

	return response, nil
}
