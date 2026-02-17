package audit

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	auditURL = "/api/audit/activity/"
)

func GetAuditLogList(ac *client.AlpaconClient, pageSize int, userName string, app string, model string) ([]AuditLogAttributes, error) {
	params := map[string]string{}
	if pageSize > 0 {
		params["page_size"] = fmt.Sprintf("%d", pageSize)
	}
	if userName != "" {
		userID, err := iam.GetUserIDByName(ac, userName)
		if err != nil {
			return nil, err
		}
		params["user"] = userID
	}
	if app != "" {
		params["app"] = app
	}
	if model != "" {
		params["model"] = model
	}

	responseBody, err := ac.SendGetRequest(utils.BuildURL(auditURL, "", params))
	if err != nil {
		return nil, err
	}

	var response api.ListResponse[AuditLogEntry]
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}

	var auditList []AuditLogAttributes
	for _, entry := range response.Results {
		auditList = append(auditList, AuditLogAttributes{
			Username:    entry.Username,
			App:         entry.App,
			Action:      entry.Action,
			Model:       entry.Model,
			StatusCode:  entry.StatusCode,
			IP:          entry.IP,
			Description: utils.TruncateString(entry.Description, 70),
			AddedAt:     utils.TimeUtils(entry.AddedAt),
		})
	}

	return auditList, nil
}
