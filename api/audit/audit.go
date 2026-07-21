package audit

import (
	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	auditURL = "/api/audit/activity/"
)

func GetAuditLogList(ac *client.AlpaconClient, tail int, userName string, app string, model string) ([]AuditLogAttributes, error) {
	params := map[string]string{}
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

	entries, err := api.FetchCursorPages[AuditLogEntry](ac, auditURL, params, tail)
	if err != nil {
		return nil, err
	}

	var auditList []AuditLogAttributes
	for _, entry := range entries {
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
