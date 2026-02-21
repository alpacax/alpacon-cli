package webftp

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type WebFTPLogEntry struct {
	AddedAt  time.Time            `json:"added_at"`
	FileName string               `json:"file_name"`
	Action   string               `json:"action"`
	Size     int64                `json:"size"`
	Success  bool                 `json:"success"`
	User     string               `json:"user"`
	Server   *types.ServerSummary `json:"server"`
	RemoteIP string               `json:"remote_ip"`
	Message  string               `json:"message"`
}

type WebFTPLogAttributes struct {
	Server   string `json:"server"`
	FileName string `json:"file_name" table:"File Name"`
	Action   string `json:"action"`
	Size     int64  `json:"size"`
	Success  bool   `json:"success"`
	User     string `json:"user"`
	RemoteIP string `json:"remote_ip" table:"Remote IP"`
	AddedAt  string `json:"added_at" table:"Added At"`
}
