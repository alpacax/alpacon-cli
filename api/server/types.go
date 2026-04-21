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
// ExpiresAt is an RFC3339 timestamp; omit to create a non-expiring token.
type RegistrationTokenRequest struct {
	Name          string   `json:"name"`
	AllowedGroups []string `json:"allowed_groups,omitempty"`
	ExpiresAt     *string  `json:"expires_at,omitempty"`
}

// RegistrationTokenCreatedResponse holds the result after creating a server registration token.
// The Key field is only returned once at creation time.
type RegistrationTokenCreatedResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AllowedGroups []string `json:"allowed_groups"`
	Key           string   `json:"key"`
	ExpiresAt     *string  `json:"expires_at"`
	AddedAt       string   `json:"added_at"`
}

// RegistrationTokenDetails is used when listing or looking up existing tokens.
// The Key field is not included—it is only available at creation time.
type RegistrationTokenDetails struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AllowedGroups []string `json:"allowed_groups"`
	Enabled       bool     `json:"enabled"`
	ExpiresAt     *string  `json:"expires_at"`
	AddedAt       string   `json:"added_at"`
}

// RegistrationTokenAttributes is the table/JSON projection used by 'server token ls'.
// AllowedGroups is rendered as a comma-separated list of group names (UUIDs when the name cannot be resolved),
// and ExpiresAt is rendered as the raw timestamp or "never" when the token does not expire.
type RegistrationTokenAttributes struct {
	Name          string `json:"name"`
	AllowedGroups string `json:"allowed_groups" table:"Allowed Groups"`
	ExpiresAt     string `json:"expires_at" table:"Expires At"`
	Enabled       bool   `json:"enabled"`
}

// RegistrationMethodGuideRequest is the request body for the guide API.
type RegistrationMethodGuideRequest struct {
	Platform          string `json:"platform"`
	ServerName        string `json:"server_name,omitempty"`
	RegistrationToken string `json:"registration_token,omitempty"`
}

// RegistrationMethodGuideJsonResponse is the structured JSON response from the guide API.
type RegistrationMethodGuideJsonResponse struct {
	MethodID         string   `json:"method_id"`
	Platform         string   `json:"platform"`
	PlatformLabel    string   `json:"platform_label"`
	AlpaconURL       string   `json:"alpacon_url"`
	PackageProxy     *string  `json:"package_proxy"`
	AllowSudoWithMFA bool     `json:"allow_sudo_with_mfa"`
	TokenKey         string   `json:"token_key"`
	ServerName       string   `json:"server_name"`
	InstallCommands  []string `json:"install_commands"`
	RegisterCommand  string   `json:"register_command"`
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
