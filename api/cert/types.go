package cert

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"time"
)

type SignRequest struct {
	DomainList  []string `json:"domain_list"`
	IpList      []string `json:"ip_list"`
	ValidDays   int      `json:"valid_days"`
	CsrText     string   `json:"csr_text"`
	RequestedBy string   `json:"requested_by"`
}

type SignRequestResponse struct {
	Id           string          `json:"id"`
	Organization string          `json:"organization"`
	CommonName   string          `json:"common_name"`
	DomainList   []string        `json:"domain_list"`
	IpList       []string        `json:"ip_list"`
	ValidDays    int             `json:"valid_days"`
	Status       string          `json:"status"`
	RequestedIp  string          `json:"requested_ip"`
	RequestedBy  iam.UserSummary `json:"requested_by"`
	SubmitURL    string          `json:"submit_url"`
}

type AuthorityRequest struct {
	Name             string `json:"name"`
	Organization     string `json:"organization"`
	Domain           string `json:"domain"`
	RootValidDays    int    `json:"root_valid_days"`
	DefaultValidDays int    `json:"default_valid_days"`
	MaxValidDays     int    `json:"max_valid_days"`
	Agent            string `json:"agent"`
	Owner            string `json:"owner"`
	Install          bool   `json:"install"`
}

type AuthorityCreateResponse struct {
	Id               string          `json:"id"`
	Name             string          `json:"name"`
	Organization     string          `json:"organization"`
	Domain           string          `json:"domain"`
	RootValidDays    int             `json:"root_valid_days"`
	DefaultValidDays int             `json:"default_valid_days"`
	MaxValidDays     int             `json:"max_valid_days"`
	Agent            string          `json:"agent"`
	Owner            iam.UserSummary `json:"owner"`
	Instruction      string          `json:"instruction"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type AuthorityResponse struct {
	Id               string          `json:"id"`
	Name             string          `json:"name"`
	Organization     string          `json:"organization"`
	Domain           string          `json:"domain"`
	RootValidDays    int             `json:"root_valid_days"`
	DefaultValidDays int             `json:"default_valid_days"`
	MaxValidDays     int             `json:"max_valid_days"`
	Agent            string          `json:"agent"`
	AgentName        string          `json:"agent_name"`
	Owner            iam.UserSummary `json:"owner"`
	UpdatedAt        time.Time       `json:"updated_at"`
	SignedAt         time.Time       `json:"signed_at"`
	ExpiresAt        time.Time       `json:"expires_at"`
}

type AuthorityAttributes struct {
	Id               string `json:"id"`
	Name             string `json:"name"`
	Organization     string `json:"organization"`
	Domain           string `json:"domain"`
	RootValidDays    int    `json:"root_valid_days"`
	DefaultValidDays int    `json:"default_valid_days"`
	MaxValidDays     int    `json:"max_valid_days"`
	Server           string `json:"server"`
	Owner            string `json:"owner"`
	SignedAt         string `json:"signed_at"`
}

type AuthorityDetails struct {
	Id               string          `json:"id"`
	Name             string          `json:"name"`
	Organization     string          `json:"organization"`
	Domain           string          `json:"domain"`
	Storage          string          `json:"storage"`
	CrtText          string          `json:"crt_text"`
	RootValidDays    int             `json:"root_valid_days"`
	DefaultValidDays int             `json:"default_valid_days"`
	MaxValidDays     int             `json:"max_valid_days"`
	RemoteIp         string          `json:"remote_ip"`
	IsConnected      bool            `json:"is_connected"`
	Status           string          `json:"status"`
	Agent            string          `json:"agent"`
	AgentName        string          `json:"agent_name"`
	Owner            iam.UserSummary `json:"owner"`
	UpdatedAt        time.Time       `json:"updated_at"`
	SignedAt         time.Time       `json:"signed_at"`
	ExpiresAt        time.Time       `json:"expires_at"`
}

type AuthoritySummary struct {
	Id    string          `json:"id"`
	Name  string          `json:"name"`
	Owner iam.UserSummary `json:"owner"`
}

type SignRequestDetail struct {
	Id         string `json:"id"`
	CommonName string `json:"common_name"`
	Status     string `json:"status"`
	CrtText    string `json:"crt_text"`
}

type CSRSubmit struct {
	CsrText string `json:"csr_text"`
}

type CSRResponse struct {
	Id          string            `json:"id"`
	Authority   AuthorityResponse `json:"authority"`
	CommonName  string            `json:"common_name"`
	DomainList  []string          `json:"domain_list"`
	IpList      []string          `json:"ip_list"`
	ValidDays   int               `json:"valid_days"`
	Status      string            `json:"status"`
	RequestedIp string            `json:"requested_ip"`
	RequestedBy iam.UserSummary   `json:"requested_by"`
	AddedAt     time.Time         `json:"added_at"`
}

type CSRAttributes struct {
	Id            string   `json:"id"`
	Name          string   `json:"name"` // Derived from the first domain in the CSR domain list
	Authority     string   `json:"authority"`
	DomainList    []string `json:"domain_list"`
	IpList        []string `json:"ip_list"`
	Status        string   `json:"status"`
	RequestedIp   string   `json:"requested_ip"`
	RequestedBy   string   `json:"requested_by"`
	RequestedDate string   `json:"requested_date"`
}

type Certificate struct {
	Id        string           `json:"id"`
	Authority AuthoritySummary `json:"authority"`
	Csr       string           `json:"csr"`
	CrtText   string           `json:"crt_text"`
	ValidDays int              `json:"valid_days"`
	SignedAt  time.Time        `json:"signed_at"`
	ExpiresAt time.Time        `json:"expires_at"`
	SignedBy  string           `json:"signed_by"`
	RenewedBy string           `json:"renewed_by"`
}

type CertificateAttributes struct {
	Id        string `json:"id"`
	Authority string `json:"authority"`
	Csr       string `json:"csr"`
	ValidDays int    `json:"valid_days"`
	SignedAt  string `json:"signed_at"`
	ExpiresAt string `json:"expires_at"`
	SignedBy  string `json:"signed_by"`
	RenewedBy string `json:"renewed_by"`
}
