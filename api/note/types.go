package note

import (
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

// NoteResponse is the API response type where Server and Author are nested objects.
type NoteResponse struct {
	ID      string              `json:"id"`
	Server  types.ServerSummary `json:"server"`
	Author  types.UserSummary   `json:"author"`
	Content string              `json:"content"`
	Private bool                `json:"private"`
	Pinned  bool                `json:"pinned"`
	AddedAt time.Time           `json:"added_at"`
}

// NoteDetails is the display type for PrintTable.
type NoteDetails struct {
	ID      string `json:"id"`
	Server  string `json:"server"`
	Author  string `json:"author"`
	Content string `json:"content"`
	Private bool   `json:"private"`
	Pinned  bool   `json:"pinned"`
	AddedAt string `json:"added_at"`
}

type NoteCreateRequest struct {
	Server  string `json:"server"`
	Content string `json:"content"`
	Private bool   `json:"private"`
	Pinned  bool   `json:"pinned"`
}
