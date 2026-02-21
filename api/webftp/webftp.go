package webftp

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	webftpLogURL = "/api/history/webftp-logs/"
)

func GetWebFTPLogList(ac *client.AlpaconClient, pageSize int, serverName string, userName string, action string) ([]WebFTPLogAttributes, error) {
	params := map[string]string{}
	if pageSize > 0 {
		params["page_size"] = fmt.Sprintf("%d", pageSize)
	}
	if serverName != "" {
		params["server_name"] = serverName
	}
	if userName != "" {
		params["user_name"] = userName
	}
	if action != "" {
		params["action"] = action
	}

	responseBody, err := ac.SendGetRequest(utils.BuildURL(webftpLogURL, "", params))
	if err != nil {
		return nil, err
	}

	var response api.ListResponse[WebFTPLogEntry]
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}

	var logList []WebFTPLogAttributes
	for _, entry := range response.Results {
		entryServerName := ""
		if entry.Server != nil {
			entryServerName = entry.Server.Name
		}
		entryUserName := ""
		if entry.User != nil {
			entryUserName = entry.User.Name
		}
		logList = append(logList, WebFTPLogAttributes{
			Server:   entryServerName,
			FileName: entry.FileName,
			Action:   entry.Action,
			Size:     entry.Size,
			Success:  entry.Success,
			User:     entryUserName,
			RemoteIP: entry.RemoteIP,
			AddedAt:  utils.TimeUtils(entry.AddedAt),
		})
	}

	return logList, nil
}
