package auth

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const serviceTokenURL = "/api/auth/service-tokens/tokens/"

type serviceTokenSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ResolveServiceTokenID returns the id of the service token nameOrID names. An
// id passes through, so a value copied from a listing needs no lookup.
func ResolveServiceTokenID(ac *client.AlpaconClient, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if utils.IsUUID(nameOrID) {
		return nameOrID, nil
	}

	result, err := api.ResolveByName[serviceTokenSummary](ac, api.ResolveByNameOptions{
		Endpoint:    serviceTokenURL,
		FilterKey:   "name",
		Name:        nameOrID,
		BlankMsg:    "service token name is required",
		NotFoundMsg: "no service token found with the given name",
	})
	if err != nil {
		return "", err
	}

	pathID, err := api.RequireResponseID(result.ID, "service token")
	if err != nil {
		return "", err
	}
	return pathID.String(), nil
}

// GetServiceTokenNames maps every service token's id to its name.
func GetServiceTokenNames(ac *client.AlpaconClient) (map[string]string, error) {
	tokens, err := api.FetchAllPages[serviceTokenSummary](ac, serviceTokenURL, nil)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(tokens))
	for _, token := range tokens {
		names[token.ID] = token.Name
	}
	return names, nil
}
