package cert

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type SignRequest struct {
	DomainList  []string `json:"domain_list"`
	IpList      []string `json:"ip_list"`
	ValidDays   int      `json:"valid_days"`
	CsrText     string   `json:"csr_text"`
	RequestedBy string   `json:"requested_by"`
}

type SignRequestResponse struct {
	ID           string            `json:"id"`
	Organization string            `json:"organization"`
	CommonName   string            `json:"common_name"`
	DomainList   []string          `json:"domain_list"`
	IpList       []string          `json:"ip_list"`
	ValidDays    int               `json:"valid_days"`
	Status       string            `json:"status"`
	RequestedIp  string            `json:"requested_ip"`
	RequestedBy  types.UserSummary `json:"requested_by"`
	SubmitURL    string            `json:"submit_url"`
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
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Organization     string               `json:"organization"`
	Domain           string               `json:"domain"`
	RootValidDays    int                  `json:"root_valid_days"`
	DefaultValidDays int                  `json:"default_valid_days"`
	MaxValidDays     int                  `json:"max_valid_days"`
	Agent            types.ServerSummary  `json:"agent"`
	Owner            types.UserSummary    `json:"owner"`
	Instruction      string               `json:"instruction"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

type AuthorityResponse struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Organization     string              `json:"organization"`
	Domain           string              `json:"domain"`
	RootValidDays    int                 `json:"root_valid_days"`
	DefaultValidDays int                 `json:"default_valid_days"`
	MaxValidDays     int                 `json:"max_valid_days"`
	Agent            types.ServerSummary `json:"agent"`
	Owner            types.UserSummary   `json:"owner"`
	UpdatedAt        time.Time           `json:"updated_at"`
	SignedAt         time.Time           `json:"signed_at"`
	ExpiresAt        time.Time           `json:"expires_at"`
}

type AuthorityAttributes struct {
	ID               string `json:"id" table:"ID"`
	Name             string `json:"name"`
	Organization     string `json:"organization"`
	Domain           string `json:"domain"`
	RootValidDays    int    `json:"root_valid_days" table:"Root Valid Days"`
	DefaultValidDays int    `json:"default_valid_days" table:"Default Valid Days"`
	MaxValidDays     int    `json:"max_valid_days" table:"Max Valid Days"`
	Server           string `json:"server"`
	Owner            string `json:"owner"`
	SignedAt         string `json:"signed_at" table:"Signed At"`
}

type AuthorityDetails struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Organization     string              `json:"organization"`
	Domain           string              `json:"domain"`
	Storage          string              `json:"storage"`
	CrtText          string              `json:"crt_text"`
	RootValidDays    int                 `json:"root_valid_days"`
	DefaultValidDays int                 `json:"default_valid_days"`
	MaxValidDays     int                 `json:"max_valid_days"`
	RemoteIp         string              `json:"remote_ip"`
	IsConnected      bool                `json:"is_connected"`
	Status           string              `json:"status"`
	Agent            types.ServerSummary `json:"agent"`
	Owner            types.UserSummary   `json:"owner"`
	UpdatedAt        time.Time           `json:"updated_at"`
	SignedAt         time.Time           `json:"signed_at"`
	ExpiresAt        time.Time           `json:"expires_at"`
}

type AuthoritySummary struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Owner types.UserSummary `json:"owner"`
}

type SignRequestDetail struct {
	ID         string `json:"id"`
	CommonName string `json:"common_name"`
	Status     string `json:"status"`
	CrtText    string `json:"crt_text"`
}

type CSRSubmit struct {
	CsrText string `json:"csr_text"`
}

type CSRResponse struct {
	ID          string           `json:"id"`
	Authority   AuthoritySummary `json:"authority"`
	CommonName  string            `json:"common_name"`
	DomainList  []string          `json:"domain_list"`
	IpList      []string          `json:"ip_list"`
	ValidDays   int               `json:"valid_days"`
	Status      string            `json:"status"`
	RequestedIp string            `json:"requested_ip"`
	RequestedBy types.UserSummary `json:"requested_by"`
	AddedAt     time.Time         `json:"added_at"`
}

type CSRAttributes struct {
	ID            string   `json:"id" table:"ID"`
	Name          string   `json:"name"` // Derived from the first domain in the CSR domain list
	Authority     string   `json:"authority"`
	DomainList    []string `json:"domain_list" table:"Domain List"`
	IpList        []string `json:"ip_list" table:"IP List"`
	Status        string   `json:"status"`
	RequestedIp   string   `json:"requested_ip" table:"Requested IP"`
	RequestedBy   string   `json:"requested_by" table:"Requested By"`
	RequestedDate string   `json:"requested_date" table:"Requested Date"`
}

type Certificate struct {
	ID        string           `json:"id"`
	Authority AuthoritySummary `json:"authority"`
	Csr       string           `json:"csr"`
	CrtText   string           `json:"crt_text"`
	ValidDays int              `json:"valid_days"`
	SignedAt  time.Time        `json:"signed_at"`
	ExpiresAt time.Time        `json:"expires_at"`
	SignedBy  string           `json:"signed_by"`
	RenewedBy string           `json:"renewed_by"`
}

type CRLResponse struct {
	CrlText   string    `json:"crl_text"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RevokeRequestCreate struct {
	Certificate     string `json:"certificate"`
	Reason          int    `json:"reason"`
	RequestedReason string `json:"requested_reason,omitempty"`
}

type RevokeRequestResponse struct {
	ID              string            `json:"id"`
	Certificate     string            `json:"certificate"`
	Authority       AuthoritySummary  `json:"authority"`
	CommonName      string            `json:"common_name"`
	SerialNumber    string            `json:"serial_number"`
	Reason          int               `json:"reason"`
	Status          string            `json:"status"`
	RequestedReason string            `json:"requested_reason"`
	RequestedBy     types.UserSummary `json:"requested_by"`
	AddedAt         time.Time         `json:"added_at"`
	ErrorMessage    string            `json:"error_message"`
	HandledAt       *time.Time        `json:"handled_at"`
}

type RevokeRequestAttributes struct {
	ID           string `json:"id" table:"ID"`
	CommonName   string `json:"common_name" table:"Common Name"`
	Authority    string `json:"authority"`
	SerialNumber string `json:"serial_number" table:"Serial Number"`
	Status       string `json:"status"`
	RequestedBy  string `json:"requested_by" table:"Requested By"`
	AddedAt      string `json:"added_at" table:"Added At"`
}

type CertificateAttributes struct {
	ID        string `json:"id" table:"ID"`
	Authority string `json:"authority"`
	Csr       string `json:"csr" table:"CSR"`
	ValidDays int    `json:"valid_days" table:"Valid Days"`
	SignedAt  string `json:"signed_at" table:"Signed At"`
	ExpiresAt string `json:"expires_at" table:"Expires At"`
	SignedBy  string `json:"signed_by" table:"Signed By"`
	RenewedBy string `json:"renewed_by" table:"Renewed By"`
}
