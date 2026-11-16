package log

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/utils"
)

type LogAttributes struct {
	ID      int             `json:"id" table:"-"`
	Program string          `json:"program"`
	Level   string          `json:"level"`
	Message string          `json:"message"`
	Date    utils.Timestamp `json:"date"`
}

type LogEntry struct {
	ID      int                  `json:"id"`
	AddedAt time.Time            `json:"added_at"`
	Date    time.Time            `json:"date"`
	Program string               `json:"program"`
	Level   int                  `json:"level"`
	Name    string               `json:"name"`
	Path    string               `json:"path"`
	LineNo  int                  `json:"lineno"`
	PID     int                  `json:"pid"`
	TID     int                  `json:"tid"`
	Process string               `json:"process"`
	Thread  string               `json:"thread"`
	Msg     string               `json:"msg"`
	Server  *types.ServerSummary `json:"server"`
}
