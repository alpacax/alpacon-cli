package types

// UserSummary is the nested user object returned in API responses.
type UserSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ServerSummary is the nested server object returned in API responses.
type ServerSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	OS          *string `json:"os"`
	IsConnected bool    `json:"is_connected"`
}
