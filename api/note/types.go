package note

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
)

// NoteResponse is the API response type where Server and Author are nested objects.
type NoteResponse struct {
	ID      string            `json:"id"`
	Server  server.ServerInfo `json:"server"`
	Author  iam.UserSummary   `json:"author"`
	Content string            `json:"content"`
	Private bool              `json:"private"`
}

// NoteDetails is the display type for PrintTable.
type NoteDetails struct {
	ID      string `json:"id"`
	Server  string `json:"server"`
	Author  string `json:"author"`
	Content string `json:"content"`
	Private bool   `json:"private"`
}

type NoteCreateRequest struct {
	Server  string `json:"server"`
	Content string `json:"content"`
	Private bool   `json:"private"`
	Pinned  bool   `json:"pinned"`
}
