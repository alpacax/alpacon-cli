package approval

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	approvalURL   = "/api/approvals/approvals/"
	myRequestsURL = "/api/approvals/approvals/-/"
)

func ListApprovalRequests(ac *client.AlpaconClient, status, requestType string) ([]ApprovalRequestAttributes, error) {
	return fetchApprovalList(ac, approvalURL, status, requestType)
}

func ListMyApprovalRequests(ac *client.AlpaconClient, status, requestType string) ([]ApprovalRequestAttributes, error) {
	return fetchApprovalList(ac, myRequestsURL, status, requestType)
}

func fetchApprovalList(ac *client.AlpaconClient, endpoint, status, requestType string) ([]ApprovalRequestAttributes, error) {
	params := map[string]string{}
	if status != "" {
		params["status"] = status
	}
	if requestType != "" {
		params["request_type"] = requestType
	}

	requests, err := api.FetchAllPages[ApprovalRequest](ac, endpoint, params)
	if err != nil {
		return nil, err
	}

	var result []ApprovalRequestAttributes
	for i := range requests {
		result = append(result, projectAttributes(&requests[i]))
	}
	return result, nil
}

func projectAttributes(r *ApprovalRequest) ApprovalRequestAttributes {
	requestedBy := ""
	if r.RequestedBy != nil {
		requestedBy = r.RequestedBy.Name
	}
	return ApprovalRequestAttributes{
		ID:          r.ID,
		Type:        r.RequestType,
		Status:      r.Status,
		RequestData: utils.TruncateString(r.RequestData, 50),
		RequestedBy: requestedBy,
		AddedAt:     r.AddedAt.Local().Format("2006-01-02 15:04"),
	}
}

func GetApprovalRequest(ac *client.AlpaconClient, id string) (*ApprovalRequest, error) {
	pathID, err := api.RequireID(id, "approval request")
	if err != nil {
		return nil, err
	}
	body, err := ac.SendGetRequest(api.IDURL(approvalURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}
	var req ApprovalRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func GetApprovalRequestRaw(ac *client.AlpaconClient, id string) ([]byte, error) {
	pathID, err := api.RequireID(id, "approval request")
	if err != nil {
		return nil, err
	}
	return ac.SendGetRequest(api.IDURL(approvalURL, pathID, "", nil))
}

func CancelRequest(ac *client.AlpaconClient, id string) error {
	pathID, err := api.RequireID(id, "approval request")
	if err != nil {
		return err
	}
	_, err = ac.SendPostRequest(api.IDURL(approvalURL, pathID, "cancel", nil), struct{}{})
	return err
}

// FindCommandApprovalRequestID reads the requester's pending command approvals:
// the command detail does not carry the approval request's id.
func FindCommandApprovalRequestID(ac *client.AlpaconClient, commandID string) (string, error) {
	if _, err := api.RequireID(commandID, "command"); err != nil {
		return "", err
	}
	params := map[string]string{
		"status": "pending", "request_type": "command_exec", "page_size": "100",
	}
	for page := 1; ; page++ {
		params["page"] = strconv.Itoa(page)
		body, err := ac.SendGetRequest(utils.BuildURL(myRequestsURL, "", params))
		if err != nil {
			// This optional lookup may be unavailable to the viewer or on an older server.
			if status := utils.HTTPStatusCode(err); status == http.StatusForbidden || status == http.StatusNotFound {
				return "", nil
			}
			return "", fmt.Errorf("fetching approval page %d: %w", page, err)
		}
		var response api.ListResponse[ApprovalRequest]
		if err := json.Unmarshal(body, &response); err != nil {
			return "", err
		}
		for _, request := range response.Results {
			if request.Metadata == nil {
				continue
			}
			matchedCommandID := request.Metadata.CommandID
			if request.Metadata.Command != nil && request.Metadata.Command.CommandID != "" {
				matchedCommandID = request.Metadata.Command.CommandID
			}
			if matchedCommandID == commandID {
				id, err := api.RequireResponseID(request.ID, "approval request")
				if err != nil {
					return "", err
				}
				return id.String(), nil
			}
		}
		if response.Next == 0 || len(response.Results) == 0 {
			return "", nil
		}
	}
}
