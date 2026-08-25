package client

import (
	"net/http"
	"sync"
)

type AlpaconClient struct {
	HTTPClient  *http.Client
	BaseURL     string
	Token       string
	AccessToken string
	Privileges  string
	Username    string
	UserAgent   string

	// tokenMu guards AccessToken, which sendRequest renews mid-flight when the
	// server reports it stale. No package outside this one reads the field.
	tokenMu  sync.Mutex
	loadOnce sync.Once
	loadErr  error
}

type CheckPrivilegesResponse struct {
	Username    string `json:"username"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}
