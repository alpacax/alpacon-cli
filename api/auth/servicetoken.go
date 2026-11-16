package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const serviceTokenURL = "/api/auth/service-tokens/tokens/"

type ServiceToken struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Application *WhoamiApplication `json:"application"`
}

// ErrNotAServiceToken reports an id that names no service token in the
// workspace, such as a personal API token's.
var ErrNotAServiceToken = errors.New("no service token in this workspace has that ID")

// ResolveServiceTokenID returns a service token's id. An id is confirmed by a
// lookup, so a personal API token's is refused; a name must match exactly once.
func ResolveServiceTokenID(ac *client.AlpaconClient, nameOrID string) (string, error) {
	token, err := ResolveServiceToken(ac, nameOrID)
	return token.ID, err
}

// ResolveServiceToken reads a service token and its bound application.
func ResolveServiceToken(ac *client.AlpaconClient, nameOrID string) (ServiceToken, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if utils.IsUUID(nameOrID) {
		return confirmServiceTokenID(ac, nameOrID)
	}

	name, err := api.RequireName(nameOrID, "service token name is required")
	if err != nil {
		return ServiceToken{}, err
	}
	tokens, err := api.FetchAllPages[ServiceToken](ac, serviceTokenURL, map[string]string{"name": name})
	if err != nil {
		return ServiceToken{}, err
	}

	// Compared here rather than trusted to the server's filter, so a filter
	// that matched loosely cannot land an entry on another token.
	var matches []ServiceToken
	for _, token := range tokens {
		if token.Name == name {
			matches = append(matches, token)
		}
	}
	switch len(matches) {
	case 0:
		return ServiceToken{}, errors.New("no service token found with the given name")
	case 1:
		pathID, err := api.RequireResponseID(matches[0].ID, "service token")
		if err != nil {
			return ServiceToken{}, err
		}
		matches[0].ID = pathID.String()
		return matches[0], nil
	default:
		return ServiceToken{}, fmt.Errorf("%d service tokens are named %q; pass the ID instead", len(matches), name)
	}
}

func confirmServiceTokenID(ac *client.AlpaconClient, id string) (ServiceToken, error) {
	pathID, err := api.RequireID(id, "service token")
	if err != nil {
		return ServiceToken{}, err
	}
	body, err := ac.SendGetRequest(api.IDURL(serviceTokenURL, pathID, "", nil))
	if err != nil {
		if utils.HTTPStatusCode(err) == http.StatusNotFound {
			return ServiceToken{}, ErrNotAServiceToken
		}
		return ServiceToken{}, err
	}
	var token ServiceToken
	if err = json.Unmarshal(body, &token); err != nil {
		return ServiceToken{}, err
	}
	token.ID = pathID.String()
	return token, nil
}

// GetServiceTokens lists the workspace's service tokens and their applications.
func GetServiceTokens(ac *client.AlpaconClient) ([]ServiceToken, error) {
	return api.FetchAllPages[ServiceToken](ac, serviceTokenURL, nil)
}
