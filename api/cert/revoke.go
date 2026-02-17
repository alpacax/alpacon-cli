package cert

import (
	"bytes"
	"encoding/json"
	"path"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	revokeRequestURL = "/api/cert/revoke-requests/"
)

func GetRevokeRequestList(ac *client.AlpaconClient, status string, certificate string) ([]RevokeRequestAttributes, error) {
	params := map[string]string{}
	if status != "" {
		params["status"] = status
	}
	if certificate != "" {
		params["certificate"] = certificate
	}

	requests, err := api.FetchAllPages[RevokeRequestResponse](ac, revokeRequestURL, params)
	if err != nil {
		return nil, err
	}

	var requestList []RevokeRequestAttributes
	for _, req := range requests {
		requestList = append(requestList, RevokeRequestAttributes{
			ID:           req.ID,
			CommonName:   req.CommonName,
			Authority:    req.Authority.Name,
			SerialNumber: req.SerialNumber,
			Status:       req.Status,
			RequestedBy:  req.RequestedBy.Name,
			AddedAt:      utils.TimeUtils(req.AddedAt),
		})
	}

	return requestList, nil
}

func GetRevokeRequestDetail(ac *client.AlpaconClient, requestId string) ([]byte, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(revokeRequestURL, requestId, nil))
	if err != nil {
		return nil, err
	}

	return body, nil
}

func CreateRevokeRequest(ac *client.AlpaconClient, request RevokeRequestCreate) (RevokeRequestResponse, error) {
	var response RevokeRequestResponse
	responseBody, err := ac.SendPostRequest(revokeRequestURL, request)
	if err != nil {
		return RevokeRequestResponse{}, err
	}

	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return RevokeRequestResponse{}, err
	}

	return response, nil
}

func ApproveRevokeRequest(ac *client.AlpaconClient, requestId string) ([]byte, error) {
	relativePath := path.Join(requestId, "approve")
	responseBody, err := ac.SendPostRequest(utils.BuildURL(revokeRequestURL, relativePath, nil), bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func DenyRevokeRequest(ac *client.AlpaconClient, requestId string) ([]byte, error) {
	relativePath := path.Join(requestId, "deny")
	responseBody, err := ac.SendPostRequest(utils.BuildURL(revokeRequestURL, relativePath, nil), bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func RetryRevokeRequest(ac *client.AlpaconClient, requestId string) ([]byte, error) {
	relativePath := path.Join(requestId, "retry")
	responseBody, err := ac.SendPostRequest(utils.BuildURL(revokeRequestURL, relativePath, nil), bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func CancelRevokeRequest(ac *client.AlpaconClient, requestId string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(revokeRequestURL, requestId, nil))
	if err != nil {
		return err
	}

	return nil
}
