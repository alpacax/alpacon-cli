package webftp

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	webftpLogURL = "/api/history/webftp-logs/"
)

func GetWebFTPLogList(ac *client.AlpaconClient, tail int, serverName string, userName string, action string) ([]WebFTPLogAttributes, error) {
	params := map[string]string{}
	if serverName != "" {
		serverID, err := server.GetServerIDByName(ac, serverName)
		if err != nil {
			return nil, fmt.Errorf("--server %q: %w", serverName, err)
		}
		params["server"] = serverID
	}
	if userName != "" {
		userID, err := iam.GetUserIDByName(ac, userName)
		if err != nil {
			return nil, fmt.Errorf("--user %q: %w", userName, err)
		}
		params["user"] = userID
	}
	if action != "" {
		params["action"] = action
	}

	// The error is carried past the projection, not returned ahead of it: a cursor walk
	// that failed part-way still hands back the entries it read, and dropping them here
	// would leave the caller the same nothing it used to get.
	entries, err := api.FetchCursorPages[WebFTPLogEntry](ac, webftpLogURL, params, tail)

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

	return logList, err
}
