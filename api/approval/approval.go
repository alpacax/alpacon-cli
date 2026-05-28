package approval

import (
	"encoding/json"
	"path"

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
	body, err := ac.SendGetRequest(utils.BuildURL(approvalURL, id, nil))
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
	return ac.SendGetRequest(utils.BuildURL(approvalURL, id, nil))
}

func ApproveRequest(ac *client.AlpaconClient, id string, opts ApproveOptions) error {
	_, err := ac.SendPostRequest(utils.BuildURL(approvalURL, path.Join(id, "approve"), nil), opts)
	return err
}

func RejectRequest(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(approvalURL, path.Join(id, "reject"), nil), struct{}{})
	return err
}

func CancelRequest(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(approvalURL, path.Join(id, "cancel"), nil), struct{}{})
	return err
}
