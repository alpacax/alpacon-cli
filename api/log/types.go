package log

import "time"

type LogAttributes struct {
	Program string `json:"program"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Date    string `json:"date"`
}

type LogEntry struct {
	ID         int       `json:"id"`
	AddedAt    time.Time `json:"added_at"`
	Date       time.Time `json:"date"`
	Program    string    `json:"program"`
	Level      int       `json:"level"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	LineNo     int       `json:"lineno"`
	PID        int       `json:"pid"`
	TID        int       `json:"tid"`
	Process    string    `json:"process"`
	Thread     string    `json:"thread"`
	Msg        string    `json:"msg"`
	Server     string    `json:"server"`
	ServerName string    `json:"server_name"`
}
