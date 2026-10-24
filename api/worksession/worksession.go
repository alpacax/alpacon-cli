package worksession

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const workSessionURL = "/api/work-sessions/sessions/"

func GetWorkSessionList(ac *client.AlpaconClient, status, requesterType, assignedUser string) ([]WorkSessionAttributes, error) {
	params := map[string]string{}
	if status != "" {
		params["status"] = status
	}
	if requesterType != "" {
		params["requester_type"] = requesterType
	}
	if assignedUser != "" {
		params["assigned_user"] = assignedUser
	}

	sessions, err := api.FetchAllPages[WorkSession](ac, workSessionURL, params)
	if err != nil {
		return nil, err
	}

	var result []WorkSessionAttributes
	for i := range sessions {
		result = append(result, ProjectAttributes(&sessions[i]))
	}
	return result, nil
}

// ProjectAttributes converts a full WorkSession into the WorkSessionAttributes
// shape used by table outputs (ls, current). Single source of truth for column projection.
func ProjectAttributes(ws *WorkSession) WorkSessionAttributes {
	serverNames := make([]string, len(ws.Servers))
	for i, srv := range ws.Servers {
		serverNames[i] = srv.Name
	}
	return WorkSessionAttributes{
		ID:          ws.ID,
		Description: utils.TruncateString(ws.Description, 70),
		Status:      ws.Status,
		Scopes:      strings.Join(ws.Scopes, ", "),
		Servers:     strings.Join(serverNames, ", "),
		ExpiresAt:   ws.ExpiresAt.Local().Format("2006-01-02 15:04"),
	}
}

func CreateWorkSession(ac *client.AlpaconClient, req WorkSessionCreateRequest) (*WorkSession, error) {
	body, err := ac.SendPostRequest(workSessionURL, req)
	if err != nil {
		return nil, err
	}
	var session WorkSession
	if err = json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	pathID, err := api.RequireResponseID(session.ID, "work session")
	if err != nil {
		return nil, fmt.Errorf("the server created the work session, but its ID is unusable (%w); check for it before creating another", err)
	}
	session.ID = pathID.String()
	return &session, nil
}

// UpdateWorkSession PATCHes a work session. Used to attach sudo policies to an
// existing session (e.g. after an 'exec' sudo was denied). The server
// may queue the change for approval, in which case it takes effect only once
// approved.
func UpdateWorkSession(ac *client.AlpaconClient, id string, req WorkSessionUpdateRequest) (*WorkSession, error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return nil, err
	}
	body, err := ac.SendPatchRequest(api.IDURL(workSessionURL, pathID, "", nil), req)
	if err != nil {
		return nil, err
	}
	var session WorkSession
	if err = json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func GetWorkSession(ac *client.AlpaconClient, id string) (*WorkSession, error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return nil, err
	}
	body, err := ac.SendGetRequest(api.IDURL(workSessionURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}
	var session WorkSession
	if err = json.Unmarshal(body, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func ActivateWorkSession(ac *client.AlpaconClient, id string) error {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return err
	}
	_, err = ac.SendPostRequest(api.IDURL(workSessionURL, pathID, "activate", nil), struct{}{})
	return err
}

func CompleteWorkSession(ac *client.AlpaconClient, id string) error {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return err
	}
	_, err = ac.SendPostRequest(api.IDURL(workSessionURL, pathID, "complete", nil), struct{}{})
	return err
}

// ExtendWorkSession requests a new expires_at for a running session. The
// server answers 200 with the session already extended, or—when the
// workspace's approval policy holds this request for a decision instead of
// auto-approving it—202 with the session's PendingExtensionRequest populated
// instead. The returned status is what tells the two apart, not
// PendingExtensionRequest on its own: a 200 body can still carry a stale one
// (the workspace's approval policy changed to auto-approve after the request
// was filed, or a lapsed request the periodic sweep has not reached yet), so
// a caller that branched on the field alone would report an already-applied
// extension as still pending.
func ExtendWorkSession(ac *client.AlpaconClient, id string, req WorkSessionExtendRequest) (*WorkSession, int, error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return nil, 0, err
	}
	body, status, err := ac.SendPostRequestWithStatus(api.IDURL(workSessionURL, pathID, "extend", nil), req)
	if err != nil {
		return nil, status, err
	}
	var session WorkSession
	if err = json.Unmarshal(body, &session); err != nil {
		return nil, status, err
	}
	return &session, status, nil
}

func RevokeWorkSession(ac *client.AlpaconClient, id string) error {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return err
	}
	_, err = ac.SendPostRequest(api.IDURL(workSessionURL, pathID, "revoke", nil), struct{}{})
	return err
}

// CancelWorkSession withdraws the requester's own pending session; the server restricts it to the creator/superuser and rejects non-pending sessions (unlike superuser-only RevokeWorkSession).
func CancelWorkSession(ac *client.AlpaconClient, id string) error {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return err
	}
	_, err = ac.SendPostRequest(api.IDURL(workSessionURL, pathID, "cancel", nil), struct{}{})
	return err
}

func GetWorkSessionRaw(ac *client.AlpaconClient, id string) ([]byte, error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return nil, err
	}
	return ac.SendGetRequest(api.IDURL(workSessionURL, pathID, "", nil))
}

// GetWorkSessionTimeline reads a work session's activity timeline by cursor.
// Naming `cursor`, even empty, on the first request is what holds the server to
// the paged shape, whose items carry no recordings: a Websh terminal reports its
// recording's chunk count in record_chunks_total instead, and the recording itself
// is read one terminal at a time through GetTerminalRecording. `include_records`
// rides along for a server that predates the paged route, so its whole-list answer
// leaves the recordings out too; a server that pages ignores it.
func GetWorkSessionTimeline(ac *client.AlpaconClient, id string) ([]TimelineItem, error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return nil, err
	}
	endpoint := api.IDURL(workSessionURL, pathID, "timeline", nil)
	return api.FetchAllCursorPages[TimelineItem](ac, endpoint, map[string]string{"include_records": "false"})
}

// IsTerminal reports whether a timeline item is a Websh terminal, the unit that
// `recording --index` counts. Tunnels are Websh sessions the server lists under
// their own type and have no recording, so they are not terminals.
func IsTerminal(item *TimelineItem) bool {
	return item.Type == "websh_session" && !item.IsTunnel
}

// ListTerminalSessionIDs returns the ids of a work session's Websh terminals in
// timeline order.
func ListTerminalSessionIDs(ac *client.AlpaconClient, id string) ([]string, error) {
	items, err := GetWorkSessionTimeline(ac, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for i := range items {
		if IsTerminal(&items[i]) {
			ids = append(ids, items[i].ID)
		}
	}
	return ids, nil
}

// GetTerminalRecording reads one Websh session's recording through the work
// session's records route, concatenating the chunks in order. The walk ends on a
// null `next` or an empty page; a short page is not the last one. addedAt is the
// first chunk's added_at, and found is false when the session has no recording.
func GetTerminalRecording(ac *client.AlpaconClient, id, webshSessionID string) (record, addedAt string, found bool, err error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return "", "", false, err
	}
	endpoint := api.IDURL(workSessionURL, pathID, "records", nil)
	chunks, err := api.FetchAllCursorPages[RecordChunk](ac, endpoint, map[string]string{"session": webshSessionID})
	if err != nil {
		return "", "", false, err
	}
	if len(chunks) == 0 {
		return "", "", false, nil
	}
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Record)
	}
	return b.String(), chunks[0].AddedAt, true, nil
}
