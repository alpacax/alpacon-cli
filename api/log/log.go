package log

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	getSystemLogURL = "/api/history/logs/"
)

func GetSystemLogList(ac *client.AlpaconClient, serverName string, tail int) ([]LogAttributes, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		"server": serverID,
	}

	// The error is carried past the projection, not returned ahead of it: a cursor walk
	// that failed part-way still hands back the entries it read, and dropping them here
	// would leave the caller the same nothing it used to get.
	entries, err := api.FetchCursorPages[LogEntry](ac, getSystemLogURL, params, tail)

	var logList []LogAttributes
	for _, log := range entries {
		logList = append(logList, LogAttributes{
			Program: log.Program,
			Level:   getLogLevel(log.Level),
			Message: fmt.Sprintf("[%s] %s", log.Process, log.Msg),
			Date:    utils.TimeUtils(log.Date),
		})
	}

	return logList, err
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
