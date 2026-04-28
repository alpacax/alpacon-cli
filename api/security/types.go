package security

// ── CommandACL ────────────────────────────────────────────────────────────────

type CommandAclRequest struct {
	Token     string `json:"token"`
	Command   string `json:"command"`
	Username  string `json:"username,omitempty"`
	Groupname string `json:"groupname,omitempty"`
}

type CommandAclResponse struct {
	ID        string `json:"id"         table:"ID"`
	TokenName string `json:"token_name" table:"Token"`
	Command   string `json:"command"    table:"Command"`
	Username  string `json:"username"   table:"Username"`
	Groupname string `json:"groupname"  table:"Groupname"`
}

// ── ServerACL ─────────────────────────────────────────────────────────────────

type ServerAclRequest struct {
	Token  string `json:"token"`
	Server string `json:"server"` // server UUID
}

type ServerAclBulkRequest struct {
	Token   string   `json:"token"`
	Servers []string `json:"servers"` // server UUIDs
}

type serverAclServer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type serverAclResponse struct {
	ID        string          `json:"id"`
	Token     string          `json:"token"`
	TokenName string          `json:"token_name"`
	Server    serverAclServer `json:"server"`
}

// ServerAclAttributes is the flat projection used for table output.
type ServerAclAttributes struct {
	ID         string `json:"id"          table:"ID"`
	TokenName  string `json:"token_name"  table:"Token"`
	ServerName string `json:"server_name" table:"Server"`
}

// ── FileACL actions ───────────────────────────────────────────────────────────

const (
	FileAclActionUpload   = "upload"
	FileAclActionDownload = "download"
	FileAclActionAll      = "*"
)

// ── FileACL ───────────────────────────────────────────────────────────────────

type FileAclRequest struct {
	Token     string `json:"token"`
	Path      string `json:"path"`
	Action    string `json:"action"`
	Username  string `json:"username,omitempty"`
	Groupname string `json:"groupname,omitempty"`
}

type FileAclResponse struct {
	ID        string `json:"id"         table:"ID"`
	TokenName string `json:"token_name" table:"Token"`
	Path      string `json:"path"       table:"Path"`
	Action    string `json:"action"     table:"Action"`
	Username  string `json:"username"   table:"Username"`
	Groupname string `json:"groupname"  table:"Groupname"`
}
