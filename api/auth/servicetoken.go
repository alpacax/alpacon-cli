package auth

import (
	"errors"
	"fmt"
	"net/http"
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

// ErrNotAServiceToken reports an id that names no service token in the
// workspace, such as a personal API token's.
var ErrNotAServiceToken = errors.New("no service token in this workspace has that ID")

// ResolveServiceTokenID returns the id of the service token nameOrID names. An
// id is looked up too, so a personal API token's id is refused rather than
// taken for a service token's; a name must match one token exactly.
func ResolveServiceTokenID(ac *client.AlpaconClient, nameOrID string) (string, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if utils.IsUUID(nameOrID) {
		return confirmServiceTokenID(ac, nameOrID)
	}

	name, err := api.RequireName(nameOrID, "service token name is required")
	if err != nil {
		return "", err
	}
	tokens, err := api.FetchAllPages[serviceTokenSummary](ac, serviceTokenURL, map[string]string{"name": name})
	if err != nil {
		return "", err
	}

	// Compared here rather than trusted to the server's filter, so a filter
	// that matched loosely cannot land an entry on another token.
	var matches []serviceTokenSummary
	for _, token := range tokens {
		if token.Name == name {
			matches = append(matches, token)
		}
	}
	switch len(matches) {
	case 0:
		return "", errors.New("no service token found with the given name")
	case 1:
		pathID, err := api.RequireResponseID(matches[0].ID, "service token")
		if err != nil {
			return "", err
		}
		return pathID.String(), nil
	default:
		return "", fmt.Errorf("%d service tokens are named %q; pass the ID instead", len(matches), name)
	}
}

func confirmServiceTokenID(ac *client.AlpaconClient, id string) (string, error) {
	pathID, err := api.RequireID(id, "service token")
	if err != nil {
		return "", err
	}
	if _, err = ac.SendGetRequest(api.IDURL(serviceTokenURL, pathID, "", nil)); err != nil {
		if utils.HTTPStatusCode(err) == http.StatusNotFound {
			return "", ErrNotAServiceToken
		}
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
