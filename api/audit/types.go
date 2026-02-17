package audit

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
)

type AuditLogEntry struct {
	ID          int              `json:"id"`
	User        *iam.UserSummary `json:"user"`
	Username    string           `json:"username"`
	IP          string           `json:"ip"`
	App         string           `json:"app"`
	Action      string           `json:"action"`
	StatusCode  int              `json:"status_code"`
	AddedAt     time.Time        `json:"added_at"`
	Model       string           `json:"model"`
	Description string           `json:"description"`
}

type AuditLogAttributes struct {
	Username    string `json:"username"`
	App         string `json:"app"`
	Action      string `json:"action"`
	Model       string `json:"model"`
	StatusCode  int    `json:"status_code" table:"Status Code"`
	IP          string `json:"ip" table:"IP"`
	Description string `json:"description"`
	AddedAt     string `json:"added_at" table:"Added At"`
}
