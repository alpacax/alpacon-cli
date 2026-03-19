package ftp

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type DownloadRequest struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	Server       string `json:"server"`
	Username     string `json:"username"`
	Groupname    string `json:"groupname"`
	ResourceType string `json:"resource_type"`
}

type DownloadResponse struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Path        string              `json:"path"`
	Size        int                 `json:"size"`
	Server      types.ServerSummary `json:"server"`
	User        string              `json:"user"`
	Username    string              `json:"username"`
	Groupname   string              `json:"groupname"`
	ExpiresAt   string              `json:"expires_at"`
	UploadURL   string              `json:"upload_url"`
	DownloadURL string              `json:"download_url"`
	Command     string              `json:"command"`
}

type UploadResponse struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Path      string              `json:"path"`
	Size      int                 `json:"size"`
	Server    types.ServerSummary `json:"server"`
	User      string              `json:"user"`
	Username  string              `json:"username"`
	Groupname string              `json:"groupname"`
	ExpiresAt time.Time           `json:"expires_at"`
	UploadURL string              `json:"upload_url"`
	Command   string              `json:"command"`
}

type TransferStatusResponse struct {
	Success *bool  `json:"success"`
	Message string `json:"message"`
}

type TransferErrorResponse struct {
	Code string `json:"code"`
}

type BulkUploadRequest struct {
	Names          []string `json:"names"`
	Path           string   `json:"path"`
	Server         string   `json:"server"`
	Username       string   `json:"username"`
	Groupname      string   `json:"groupname"`
	AllowOverwrite bool     `json:"allow_overwrite"`
	AllowUnzip     bool     `json:"allow_unzip"`
}

type BulkUploadTriggerRequest struct {
	IDs []string `json:"ids"`
}

type BulkDownloadRequest struct {
	Path      []string `json:"path"`
	Server    string   `json:"server"`
	Username  string   `json:"username"`
	Groupname string   `json:"groupname"`
}
