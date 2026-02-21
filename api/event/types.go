package event

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type EventAttributes struct {
	Server      string `json:"server"`
	Shell       string `json:"shell"`
	Command     string `json:"command"`
	Result      string `json:"result"`
	Status      string `json:"status"`
	Operator    string `json:"operator"`
	RequestedAt string `json:"requested_at" table:"Requested At"`
}

type EventDetails struct {
	ID            string             `json:"id"`
	Shell         string             `json:"shell"`
	Line          string             `json:"line"`
	Success       *bool              `json:"success"`
	Result        string             `json:"result"`
	Status        map[string]any     `json:"status"`
	ResponseDelay float64            `json:"response_delay"`
	ElapsedTime   float64            `json:"elapsed_time"`
	AddedAt       time.Time          `json:"added_at"`
	Server        types.ServerSummary `json:"server"`
	RequestedBy   types.UserSummary  `json:"requested_by"`
}

type CommandRequest struct {
	Shell       string            `json:"shell"`
	Line        string            `json:"line"`
	Env         map[string]string `json:"env"`
	Data        string            `json:"data"`
	Username    string            `json:"username"`
	Groupname   string            `json:"groupname"`
	ScheduledAt *time.Time        `json:"scheduled_at"`
	Server      string            `json:"server"`
	RunAfter    []string          `json:"run_after"`
}

type CommandResponse struct {
	ID          string              `json:"id"`
	Shell       string              `json:"shell"`
	Line        string              `json:"line"`
	Data        string              `json:"data"`
	Username    string              `json:"username"`
	Groupname   string              `json:"groupname"`
	AddedAt     time.Time           `json:"added_at"`
	ScheduledAt time.Time           `json:"scheduled_at"`
	Server      types.ServerSummary `json:"server"`
	RequestedBy types.UserSummary   `json:"requested_by"`
	RunAfter    []any               `json:"run_after"`
}
