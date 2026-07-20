package webftp

import (
	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	webftpLogURL = "/api/history/webftp-logs/"
)

func GetWebFTPLogList(ac *client.AlpaconClient, tail int, serverName string, userName string, action string) ([]WebFTPLogAttributes, error) {
	params := map[string]string{}
	if serverName != "" {
		params["server_name"] = serverName
	}
	if userName != "" {
		params["user_name"] = userName
	}
	if action != "" {
		params["action"] = action
	}

	entries, err := api.FetchCursorPages[WebFTPLogEntry](ac, webftpLogURL, params, tail)
	if err != nil {
		return nil, err
	}

	var logList []WebFTPLogAttributes
	for _, entry := range entries {
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
