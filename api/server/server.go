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
	tokenInstallGuideURL = "/api/servers/registration-methods/token-install/guide/"
	ansibleGuideURL      = "/api/servers/registration-methods/ansible/guide/"
	// Duplicated from api/iam; see groupSummary.
	iamGroupURL = "/api/iam/groups/"
)

// Server action identifiers accepted by the actions endpoint.
const (
	ActionUpdateInformation = "update_information"
	ActionUpgradeAgent      = "upgrade_agent"
	ActionRestartAgent      = "restart_agent"
	ActionInstallPAM        = "install_pam"
)

// ErrRegistrationTokenNotFound is returned when no registration token matches the given name.
var ErrRegistrationTokenNotFound = errors.New("no registration token found with the given name")

// groupSummary is the id and name of an IAM group, kept local so this package need not import
// api/iam and its heavier GroupResponse type.
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
	pathID, err := serverPathIDByName(ac, serverName)
	if err != nil {
		return nil, err
	}

	body, err := ac.SendGetRequest(api.IDURL(serverURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}

	return body, nil
}

func DeleteServer(ac *client.AlpaconClient, serverName string) error {
	pathID, err := serverPathIDByName(ac, serverName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(api.IDURL(serverURL, pathID, "", nil))
	if err != nil {
		return err
	}

	return nil
}

func RequestServerAction(ac *client.AlpaconClient, serverName, action string, force bool) error {
	pathID, err := serverPathIDByName(ac, serverName)
	if err != nil {
		return utils.MarkSubmission(err)
	}

	req := serverActionRequest{Action: action, Force: force}
	_, err = ac.SendPostRequest(api.IDURL(serverURL, pathID, "actions", nil), req)
	return utils.MarkSubmission(err)
}

func GetServerIDByName(ac *client.AlpaconClient, serverName string) (string, error) {
	pathID, err := serverPathIDByName(ac, serverName)
	if err != nil {
		return "", err
	}
	return pathID.String(), nil
}

func serverPathIDByName(ac *client.AlpaconClient, serverName string) (api.PathID, error) {
	result, err := api.ResolveByName[ServerDetails](ac, api.ResolveByNameOptions{
		Endpoint:    serverURL,
		FilterKey:   "name",
		Name:        serverName,
		BlankMsg:    "server name is required",
		NotFoundMsg: "no server found with the given name",
	})
	if err != nil {
		return api.PathID{}, err
	}

	return api.RequireResponseID(result.ID, "server")
}

// ResolveServerNames converts server names to their UUIDs, one request per name.
func ResolveServerNames(ac *client.AlpaconClient, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, err := GetServerIDByName(ac, name)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve server %q: %w", name, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetServerNames maps every server's id to its name.
func GetServerNames(ac *client.AlpaconClient) (map[string]string, error) {
	type serverSummary struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	servers, err := api.FetchAllPages[serverSummary](ac, serverURL, nil)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(servers))
	for _, server := range servers {
		names[server.ID] = server.Name
	}
	return names, nil
}

func UpdateServer(ac *client.AlpaconClient, serverName string) ([]byte, error) {
	pathID, err := serverPathIDByName(ac, serverName)
	if err != nil {
		return nil, err
	}

	responseBody, err := ac.SendGetRequest(api.IDURL(serverURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(api.IDURL(serverURL, pathID, "", nil), data)
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

// buildGroupUUIDToNameMap maps every IAM group's UUID to its name. On failure it warns and returns
// an empty map, so the list still renders with raw UUIDs.
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
	pathID, err := api.RequireResponseID(token.ID, "registration token")
	if err != nil {
		return err
	}
	_, err = ac.SendDeleteRequest(api.IDURL(registrationTokenURL, pathID, "", nil))
	return err
}

// GetRegistrationTokenAttributes returns every registration token projected for table/JSON display.
// Group UUIDs are resolved with one fetch of the group list; if that fails, the UUIDs are shown
// as-is.
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
	return fetchRegistrationGuide[RegistrationMethodGuideJsonResponse](ac, tokenInstallGuideURL, platform, serverName, tokenID)
}

func GetAnsibleRegistrationGuideJSON(ac *client.AlpaconClient, platform, serverName, tokenID string) (AnsibleGuideJsonResponse, error) {
	return fetchRegistrationGuide[AnsibleGuideJsonResponse](ac, ansibleGuideURL, platform, serverName, tokenID)
}

func fetchRegistrationGuide[T any](ac *client.AlpaconClient, guideURL, platform, serverName, tokenID string) (T, error) {
	var response T
	req := RegistrationMethodGuideRequest{
		Platform:          platform,
		ServerName:        serverName,
		RegistrationToken: tokenID,
	}

	url := utils.BuildURL(guideURL, "", map[string]string{"response_type": "json"})
	body, err := ac.SendPostRequest(url, req)
	if err != nil {
		return response, err
	}

	if err = json.Unmarshal(body, &response); err != nil {
		return response, err
	}

	return response, nil
}
