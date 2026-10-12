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
	// Command is the command_exec detail payload; nil for other request types.
	Command *CommandDetail `json:"command,omitempty"`
}

// CommandDetail is the part of a command_exec approval's detail payload the
// CLI renders. The full payload is available with --output json.
type CommandDetail struct {
	FileExecution *FileExecution `json:"file_execution,omitempty"`
}

// FileExecution is what the approver is asked to clear for a file execution
// request. Content is the script as the server shows it to approvers (with
// acknowledged exposures masked) and is null when the server withholds it.
type FileExecution struct {
	Content     *string  `json:"content"`
	SHA256      string   `json:"sha256"`
	Path        string   `json:"path"`
	Interpreter string   `json:"interpreter"`
	Args        []string `json:"args"`
	RunAs       string   `json:"runas"`
	RunAsGroup  string   `json:"runas_group"`
	// ReuseDays is what the requester proposed for a standing approval; null
	// when none was proposed.
	ReuseDays *int `json:"reuse_days"`
	// GrantDays is what approving with a standing approval would grant now.
	// Present only while the request is pending.
	GrantDays *int `json:"grant_days"`
}

type ApprovalRequestAttributes struct {
	ID          string `json:"id"           table:"ID"`
	Type        string `json:"request_type" table:"Type"`
	Status      string `json:"status"       table:"Status"`
	RequestData string `json:"request_data" table:"Request"`
	RequestedBy string `json:"requested_by" table:"Requested By"`
	AddedAt     string `json:"added_at"     table:"Added At"`
}
