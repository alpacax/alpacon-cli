package log

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	getSystemLogURL = "/api/history/logs/"
)

func GetSystemLogList(ac *client.AlpaconClient, serverName string, pageSize int) ([]LogAttributes, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		"server":    serverID,
		"page_size": fmt.Sprintf("%d", pageSize),
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(getSystemLogURL, "", params))
	if err != nil {
		return nil, err
	}

	var response api.ListResponse[LogEntry]
	if err = json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}

	var logList []LogAttributes
	for _, log := range response.Results {
		logList = append(logList, LogAttributes{
			Program: log.Program,
			Level:   getLogLevel(log.Level),
			Message: fmt.Sprintf("[%s] %s", log.Process, log.Msg),
			//	Date:    log.Date.Format("2006-01-02 15:04:05 MST"),
			Date: utils.TimeUtils(log.Date),
		})
	}

	return logList, nil
}

func getLogLevel(level int) string {
	switch level {
	case 10:
		return "DEBUG"
	case 20:
		return "INFO"
	case 30:
		return "WARN"
	case 40:
		return "ERROR"
	case 50:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}
