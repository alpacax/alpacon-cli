package server

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type ServerAttributes struct {
	Name      string `json:"name"`
	IP        string `json:"ip" table:"IP"`
	OS        string `json:"os" table:"OS"`
	Connected bool   `json:"connected"`
	Owner     string `json:"owner"`
}

// RegistrationTokenRequest is used to create a new server registration token.
type RegistrationTokenRequest struct {
	Name          string   `json:"name"`
	AllowedGroups []string `json:"allowed_groups,omitempty"`
}

// RegistrationTokenCreatedResponse holds the result after creating a server registration token.
// The Key field is only returned once at creation time.
type RegistrationTokenCreatedResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AllowedGroups []string `json:"allowed_groups"`
	Key           string   `json:"key"`
	AddedAt       string   `json:"added_at"`
}

// RegistrationTokenDetails is used when listing or looking up existing tokens.
// The Key field is not included—it is only available at creation time.
type RegistrationTokenDetails struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AllowedGroups []string `json:"allowed_groups"`
	Enabled       bool     `json:"enabled"`
	AddedAt       string   `json:"added_at"`
}

// RegistrationMethodGuideRequest is the request body for the guide API.
type RegistrationMethodGuideRequest struct {
	Platform          string `json:"platform"`
	ServerName        string `json:"server_name,omitempty"`
	RegistrationToken string `json:"registration_token,omitempty"`
}

// RegistrationMethodGuideResponse is the response from the guide API.
// Content contains the rendered installation guide with the actual token key embedded.
type RegistrationMethodGuideResponse struct {
	MethodID string `json:"method_id"`
	Content  string `json:"content"`
}

type ServerStatus struct {
	Code     string           `json:"code"`
	Icon     string           `json:"icon"`
	Meta     ServerStatusMeta `json:"meta"`
	Text     string           `json:"text"`
	Color    string           `json:"color"`
	Messages []string         `json:"messages"`
}

type ServerStatusMeta struct {
	Delay1d  float64 `json:"delay_1d"`
	Delay1h  float64 `json:"delay_1h"`
	Delay1w  float64 `json:"delay_1w"`
	DelayNow float64 `json:"delay_now"`
}

type ServerDetails struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	RemoteIP         string            `json:"remote_ip"`
	Status           ServerStatus      `json:"status"`
	IsConnected      bool              `json:"is_connected"`
	Commissioned     bool              `json:"commissioned"`
	Starred          bool              `json:"starred"`
	CPUPhysicalCores int               `json:"cpu_physical_cores"`
	CPULogicalCores  int               `json:"cpu_logical_cores"`
	CPUType          string            `json:"cpu_type"`
	PhysicalMemory   int64             `json:"physical_memory"`
	OSName           string            `json:"os_name"`
	OSVersion        string            `json:"os_version"`
	Load             float64           `json:"load"`
	BootTime         time.Time         `json:"boot_time"`
	Owner            types.UserSummary `json:"owner"`
	Groups           []string          `json:"groups"`
}
