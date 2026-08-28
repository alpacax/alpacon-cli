package client

import (
	"net/http"
	"sync"
)

type AlpaconClient struct {
	HTTPClient    *http.Client
	BaseURL       string
	WorkspaceName string // the workspace BaseURL points at; config can be rewritten mid-flight
	Token         string
	AccessToken   string
	Privileges    string
	Username      string
	UserAgent     string

	// tokenMu guards AccessToken, which sendRequest renews mid-flight when the
	// server reports it stale. No package outside this one reads the field.
	tokenMu sync.Mutex
	// refreshMu serializes the refresh-token grant so one expiry costs one round
	// trip. The grant is unbounded network I/O, so it runs under this lock and
	// never under tokenMu—otherwise a slow Auth0 would stall every other request
	// the client has in flight.
	refreshMu sync.Mutex
	loadOnce  sync.Once
	loadErr   error
}

type CurrentUserResponse struct {
	Username    string `json:"username"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}
