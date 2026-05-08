package worksession

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type WorkSession struct {
	ID            string                `json:"id"`
	Description   string                `json:"description"`
	Status        string                `json:"status"`
	RequesterType string                `json:"requester_type"`
	Scopes        []string              `json:"scopes"`
	Servers       []types.ServerSummary `json:"servers"`
	CreatedBy     *types.UserSummary    `json:"created_by"`
	AssignedUser  *types.UserSummary    `json:"assigned_user"`
	ExpiresAt     time.Time             `json:"expires_at"`
	StartedAt     *time.Time            `json:"started_at"`
	CompletedAt   *time.Time            `json:"completed_at"`
	AddedAt       time.Time             `json:"added_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

type WorkSessionAttributes struct {
	ID          string `json:"id"         table:"ID"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Scopes      string `json:"scopes"`
	Servers     string `json:"servers"`
	ExpiresAt   string `json:"expires_at" table:"Expires At"`
}

type WorkSessionCreateRequest struct {
	Description   string   `json:"description"`
	RequesterType string   `json:"requester_type"`
	Scopes        []string `json:"scopes"`
	Servers       []string `json:"servers"`
	ExpiresAt     string   `json:"expires_at"`
}

type WorkSessionExtendRequest struct {
	ExpiresAt string `json:"expires_at"`
}
