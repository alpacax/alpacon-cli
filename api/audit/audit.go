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

	// The error is carried past the projection, not returned ahead of it: a cursor walk
	// that failed part-way still hands back the entries it read, and dropping them here
	// would leave the caller the same nothing it used to get.
	entries, err := api.FetchCursorPages[AuditLogEntry](ac, auditURL, params, tail)

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

	return auditList, err
}
