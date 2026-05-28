package approval

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type ApprovalRequest struct {
	ID          string             `json:"id"`
	RequestType string             `json:"request_type"`
	RequestData string             `json:"request_data"`
	Description string             `json:"description"`
	Status      string             `json:"status"`
	RequestedBy *types.UserSummary `json:"requested_by"`
	ReviewedBy  *types.UserSummary `json:"reviewed_by"`
	ReviewedAt  *time.Time         `json:"reviewed_at"`
	AddedAt     time.Time          `json:"added_at"`
}

type ApprovalRequestAttributes struct {
	ID          string `json:"id"           table:"ID"`
	Type        string `json:"request_type" table:"Type"`
	Status      string `json:"status"       table:"Status"`
	RequestData string `json:"request_data" table:"Request"`
	RequestedBy string `json:"requested_by" table:"Requested By"`
	AddedAt     string `json:"added_at"     table:"Added At"`
}

type ApproveOptions struct {
	AdjustedScopes  []string `json:"adjusted_scopes,omitempty"`
	AdjustedServers []string `json:"adjusted_servers,omitempty"`
}
