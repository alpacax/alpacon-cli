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
	Metadata    *ApprovalMetadata  `json:"metadata"`
}

// ApprovalMetadata is the part of an approval's metadata the CLI renders.
type ApprovalMetadata struct {
	Command     *CommandDetail    `json:"command"`
	Sudo        *SudoDetail       `json:"sudo"`
	Intent      string            `json:"intent"`
	WorkSession *WorkSessionRef   `json:"work_session"`
	Changes     *RequestedChanges `json:"changes"`
}

// CommandDetail is the part of a command_exec approval's detail payload the
// CLI renders. The full payload is available with --output json.
type CommandDetail struct {
	Line          string `json:"line"`
	ServerName    string `json:"server_name"`
	RiskLevel     string `json:"risk_level"`
	RunsWithSudo  bool   `json:"runs_with_sudo"`
	Purpose       string `json:"purpose"`
	DeclaredAgent string `json:"declared_agent"`
	// ReviewSummary, Tier2Reasoning and Factors reach only a viewer who may decide the request.
	ReviewSummary  string         `json:"review_summary"`
	Tier2Reasoning string         `json:"tier2_reasoning"`
	Factors        []RiskFactor   `json:"factors"`
	FileExecution  *FileExecution `json:"file_execution"`
}

type RiskFactor struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// SudoDetail is the sudo approval's detail payload. Tier2Reasoning and
// DescriptionAligned reach deciders only; a null DescriptionAligned is no judgment.
type SudoDetail struct {
	Command            string `json:"command"`
	RunAs              string `json:"runas"`
	ServerName         string `json:"server_name"`
	RiskLevel          string `json:"risk_level"`
	GrantStatus        string `json:"grant_status"`
	DeclaredAgent      string `json:"declared_agent"`
	Tier2Reasoning     string `json:"tier2_reasoning"`
	DescriptionAligned *bool  `json:"description_aligned"`
}

type WorkSessionRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type RequestedChanges struct {
	Title                 string       `json:"title"`
	Description           string       `json:"description"`
	Scopes                []string     `json:"scopes"`
	Servers               []ServerRef  `json:"servers"`
	SudoPolicies          []SudoPolicy `json:"sudo_policies"`
	TargetAccountUsername string       `json:"target_account_username"`
}

type ServerRef struct {
	Name string `json:"name"`
}

type SudoPolicy struct {
	Commands []string `json:"commands"`
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
