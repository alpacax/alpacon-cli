package server

import "time"

// UserSummary is a local copy of the nested user object returned in server API responses.
// Defined here to avoid an import cycle with api/iam (which imports api/server).
type UserSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ServerInfo represents the nested server object returned in API responses.
// API responses return server as {id, name, os, is_connected} instead of a UUID string.
type ServerInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	OS          *string `json:"os"`
	IsConnected bool    `json:"is_connected"`
}

type ServerAttributes struct {
	Name      string `json:"name"`
	IP        string `json:"ip" table:"IP"`
	OS        string `json:"os" table:"OS"`
	Connected bool   `json:"connected"`
	Owner     string `json:"owner"`
}

type ServerRequest struct {
	Name     string   `json:"name"`
	Platform string   `json:"platform"`
	Groups   []string `json:"groups"`
}

type ServerCreatedResponse struct {
	Name         string   `json:"name"`
	ID           string   `json:"id"`
	Instruction1 string   `json:"instruction_1"`
	Instruction2 string   `json:"instruction_2"`
	Groups       []string `json:"groups"`
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
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	RemoteIP         string          `json:"remote_ip"`
	Status           ServerStatus    `json:"status"`
	IsConnected      bool            `json:"is_connected"`
	Commissioned     bool            `json:"commissioned"`
	Starred          bool            `json:"starred"`
	CPUPhysicalCores int             `json:"cpu_physical_cores"`
	CPULogicalCores  int             `json:"cpu_logical_cores"`
	CPUType          string          `json:"cpu_type"`
	PhysicalMemory   int64           `json:"physical_memory"`
	OSName           string          `json:"os_name"`
	OSVersion        string          `json:"os_version"`
	Load             float64         `json:"load"`
	BootTime         time.Time       `json:"boot_time"`
	Owner            UserSummary `json:"owner"`
	Groups           []string        `json:"groups"`
	GroupsName       []string        `json:"groups_name"`
}
