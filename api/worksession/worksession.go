package worksession

import (
	"encoding/json"
	"errors"
	"path"
	"strconv"
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
	return &session, nil
}

// UpdateWorkSession PATCHes a work session. Used to attach sudo policies to an
// existing session (e.g. after an 'exec' sudo was denied). The server
// may queue the change for approval, in which case it takes effect only once
// approved.
func UpdateWorkSession(ac *client.AlpaconClient, id string, req WorkSessionUpdateRequest) (*WorkSession, error) {
	body, err := ac.SendPatchRequest(utils.BuildURL(workSessionURL, id, nil), req)
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
	body, err := ac.SendGetRequest(utils.BuildURL(workSessionURL, id, nil))
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
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "activate"), nil), struct{}{})
	return err
}

func CompleteWorkSession(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "complete"), nil), struct{}{})
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
	body, status, err := ac.SendPostRequestWithStatus(utils.BuildURL(workSessionURL, path.Join(id, "extend"), nil), req)
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
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "revoke"), nil), struct{}{})
	return err
}

// CancelWorkSession withdraws the requester's own pending session; the server restricts it to the creator/superuser and rejects non-pending sessions (unlike superuser-only RevokeWorkSession).
func CancelWorkSession(ac *client.AlpaconClient, id string) error {
	_, err := ac.SendPostRequest(utils.BuildURL(workSessionURL, path.Join(id, "cancel"), nil), struct{}{})
	return err
}

func GetWorkSessionRaw(ac *client.AlpaconClient, id string) ([]byte, error) {
	return ac.SendGetRequest(utils.BuildURL(workSessionURL, id, nil))
}

// GetWorkSessionTimeline reads a work session's activity timeline.
//
// The route answers in two shapes and the request picks between them. Naming
// either `cursor` or `page_size` selects the paginated one, whose `next` is an
// opaque cursor string and whose pages carry no recording bytes at all; naming
// neither serves the whole timeline under `results`, with the recordings
// embedded. So
// includeRecords decides the shape, not just the parameter: there is no
// paginated read that comes back with recordings in it.
//
// `include_records` rides on both requests even though a page ignores it, for
// a server that does not paginate this route—it reads the parameter, and
// dropping it there would put recordings back into a read that asked for none.
func GetWorkSessionTimeline(ac *client.AlpaconClient, id string, includeRecords bool) ([]TimelineItem, error) {
	endpoint := utils.BuildURL(workSessionURL, path.Join(id, "timeline"), nil)
	params := map[string]string{"include_records": strconv.FormatBool(includeRecords)}
	if includeRecords {
		return getWholeWorkSessionTimeline(ac, endpoint, params)
	}
	return api.FetchAllCursorPages[TimelineItem](ac, endpoint, params)
}

// ErrTimelinePaginated is what the whole-timeline read answers once the route
// stops serving the shape it asks for. The two shapes are told apart by one
// key: the unpaginated answer has no `next` at all, and a paginator's always
// carries one, null on its last page included. So a `next` in the answer to a
// request that named neither `cursor` nor `page_size` means the route now
// paginates regardless, and the recordings this read exists for are gone from
// it—a page would otherwise decode as a whole session and be shown as one.
var ErrTimelinePaginated = errors.New(
	"this server paginates the work session timeline, which this version of alpacon reads only whole, " +
		"so it would show the first page as the whole session; run 'alpacon update' to install a version " +
		"that reads the timeline page by page and its recordings through the per-session recording route",
)

// getWholeWorkSessionTimeline takes the unpaginated shape in one request. It
// names neither `cursor` nor `page_size`, which is what holds the server to
// that shape, so nothing here may add either.
func getWholeWorkSessionTimeline(ac *client.AlpaconClient, endpoint string, params map[string]string) ([]TimelineItem, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(endpoint, "", params))
	if err != nil {
		return nil, err
	}
	// Neither ListResponse nor CursorListResponse: this shape has no `next` of
	// either type, and decoding through one of them would claim a paginator the
	// response does not come from. Next is read for one reason—so that a `next`
	// nothing claimed is refused rather than dropped on the floor, which is how
	// a first page would come back looking like a whole session.
	var response struct {
		Results []TimelineItem  `json:"results"`
		Next    json.RawMessage `json:"next"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	// Absent is nil; anything else is a key the unpaginated shape does not
	// have. A null one counts: it is a paginator's last page, not a whole
	// timeline, and on a session short enough to fit one page it is the only
	// thing distinguishing the two.
	if len(response.Next) > 0 {
		return nil, ErrTimelinePaginated
	}
	return response.Results, nil
}
