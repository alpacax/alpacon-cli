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
	accessToken   string
	Privileges    string
	Username      string
	UserAgent     string
	// Insecure records that this workspace was configured to skip TLS
	// verification, so a credential written for another client (a kubeconfig)
	// can say the same instead of failing where this client succeeds.
	Insecure bool
	// noRenewal skips the refresh-token renewal on a 401: it mints a workspace-
	// audience token, which a client for another audience (the signer) cannot use.
	noRenewal bool

	// tokenMu guards accessToken, which sendRequest renews mid-flight. Unexporting
	// the field keeps other packages out, token_boundary_test.go keeps this one out.
	tokenMu sync.Mutex
	// refreshMu serializes the refresh-token grant so one expiry costs one round
	// trip. The grant is unbounded network I/O, so it runs under this lock and
	// never under tokenMu—otherwise a slow Auth0 would stall every other request
	// the client has in flight.
	refreshMu sync.Mutex
	loadOnce  sync.Once
	loadErr   error
}

type RawResponse struct {
	Status     string
	StatusCode int
	Proto      string
	Header     http.Header
	Body       []byte
}

type CurrentUserResponse struct {
	Username    string `json:"username"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}
