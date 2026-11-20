package audit

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/utils"
)

type AuditLogEntry struct {
	ID          string             `json:"id"`
	User        *types.UserSummary `json:"user"`
	Username    string             `json:"username"`
	IP          string             `json:"ip"`
	App         string             `json:"app"`
	Action      string             `json:"action"`
	StatusCode  int                `json:"status_code"`
	AddedAt     time.Time          `json:"added_at"`
	Model       string             `json:"model"`
	Description string             `json:"description"`
}

type AuditLogAttributes struct {
	ID          string              `json:"id" table:"-"`
	Username    string              `json:"username"`
	App         string              `json:"app"`
	Action      string              `json:"action"`
	Model       string              `json:"model"`
	StatusCode  int                 `json:"status_code" table:"Status Code"`
	IP          string              `json:"ip" table:"IP"`
	Description utils.TruncatedText `json:"description"`
	AddedAt     utils.Timestamp     `json:"added_at" table:"Added At"`
}
