package client

import "net/http"

type AlpaconClient struct {
	HTTPClient  *http.Client
	BaseURL     string
	Token       string
	AccessToken string
	Privileges  string
	Username    string
	UserAgent   string
}

type CheckAuthResponse struct {
	Authenticated bool `json:"authenticated"`
}

type CheckPrivilegesResponse struct {
	Username    string `json:"username"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}
