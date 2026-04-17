package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	serverURL            = "/api/servers/servers/"
	registrationTokenURL = "/api/servers/registration-tokens/"
	registrationGuideURL = "/api/servers/registration-methods/token-install/guide/"
	// iamGroupURL is duplicated from api/iam so this package can build a lean
	// UUID→name projection without pulling in the full iam.GroupResponse type.
	iamGroupURL = "/api/iam/groups/"
)

// ErrRegistrationTokenNotFound is returned when no registration token matches the given name.
var ErrRegistrationTokenNotFound = errors.New("no registration token found with the given name")

// groupSummary is a minimal projection used to build a UUID→name map for group display.
// Keeping it local avoids importing api/iam and pulling in its heavier GroupResponse type.
type groupSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

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

func ListRegistrationTokens(ac *client.AlpaconClient) ([]RegistrationTokenDetails, error) {
	return api.FetchAllPages[RegistrationTokenDetails](ac, registrationTokenURL, nil)
}

// buildGroupUUIDToNameMap fetches all IAM groups and returns a UUID→name map.
// On failure it emits a one-time warning and returns an empty map so callers
// never block on a group-lookup error; the list still renders with raw UUIDs.
func buildGroupUUIDToNameMap(ac *client.AlpaconClient) map[string]string {
	groups, err := api.FetchAllPages[groupSummary](ac, iamGroupURL, nil)
	if err != nil {
		utils.CliWarning("Could not resolve group names; showing UUIDs instead: %s", err)
		return map[string]string{}
	}
	m := make(map[string]string, len(groups))
	for _, g := range groups {
		m[g.ID] = g.Name
	}
	return m
}

// DeleteRegistrationToken resolves a token name to its ID and sends a DELETE request.
func DeleteRegistrationToken(ac *client.AlpaconClient, tokenName string) error {
	token, err := GetRegistrationTokenByName(ac, tokenName)
	if err != nil {
		return err
	}
	_, err = ac.SendDeleteRequest(utils.BuildURL(registrationTokenURL, token.ID, nil))
	return err
}

// GetRegistrationTokenAttributes returns all registration tokens projected for table/JSON display.
// Group UUIDs are resolved to group names using a single batched lookup; on lookup failure,
// UUIDs are shown as-is and the overall list is not blocked.
func GetRegistrationTokenAttributes(ac *client.AlpaconClient) ([]RegistrationTokenAttributes, error) {
	tokens, err := ListRegistrationTokens(ac)
	if err != nil {
		return nil, err
	}

	needsGroups := false
	for _, t := range tokens {
		if len(t.AllowedGroups) > 0 {
			needsGroups = true
			break
		}
	}
	groupMap := map[string]string{}
	if needsGroups {
		groupMap = buildGroupUUIDToNameMap(ac)
	}

	out := make([]RegistrationTokenAttributes, 0, len(tokens))
	for _, t := range tokens {
		names := make([]string, 0, len(t.AllowedGroups))
		for _, id := range t.AllowedGroups {
			if n, ok := groupMap[id]; ok {
				names = append(names, n)
			} else {
				names = append(names, id)
			}
		}
		expires := "never"
		if t.ExpiresAt != nil {
			expires = *t.ExpiresAt
		}
		out = append(out, RegistrationTokenAttributes{
			Name:          t.Name,
			AllowedGroups: strings.Join(names, ", "),
			ExpiresAt:     expires,
			Enabled:       t.Enabled,
		})
	}
	return out, nil
}

func GetRegistrationGuideJSON(ac *client.AlpaconClient, platform, serverName, tokenID string) (RegistrationMethodGuideJsonResponse, error) {
	req := RegistrationMethodGuideRequest{
		Platform:          platform,
		ServerName:        serverName,
		RegistrationToken: tokenID,
	}

	url := utils.BuildURL(registrationGuideURL, "", map[string]string{"response_type": "json"})
	body, err := ac.SendPostRequest(url, req)
	if err != nil {
		return RegistrationMethodGuideJsonResponse{}, err
	}

	var response RegistrationMethodGuideJsonResponse
	if err = json.Unmarshal(body, &response); err != nil {
		return RegistrationMethodGuideJsonResponse{}, err
	}

	return response, nil
}
