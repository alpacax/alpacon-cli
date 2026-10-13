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

// GetWorkSessionTimeline reads a work session's activity timeline. A request
// naming `cursor`, even empty, is paginated and its pages carry no recordings;
// one naming no `cursor` gets the whole timeline with recordings and no `next`
// key. So includeRecords picks the shape. `include_records` rides on the
// paginated read too, for a server that does not paginate this route.
func GetWorkSessionTimeline(ac *client.AlpaconClient, id string, includeRecords bool) ([]TimelineItem, error) {
	endpoint := utils.BuildURL(workSessionURL, path.Join(id, "timeline"), nil)
	params := map[string]string{"include_records": strconv.FormatBool(includeRecords)}
	if includeRecords {
		return getWholeWorkSessionTimeline(ac, endpoint, params)
	}
	return api.FetchAllCursorPages[TimelineItem](ac, endpoint, params)
}

// ErrTimelinePaginated is returned when a whole-timeline read is answered with
// a `next`, so a page is never shown as the whole session.
var ErrTimelinePaginated = errors.New(
	"this server paginates the work session timeline, which this version of alpacon reads only whole, " +
		"so it would show the first page as the whole session; run 'alpacon update' to install a version " +
		"that reads the timeline page by page and its recordings through the per-session recording route",
)

// getWholeWorkSessionTimeline takes the unpaginated shape in one request. It
// names no `cursor`, which is what holds the server to that shape, so nothing
// here may add one; `page_size` stays off too, since a whole list has no page
// to size.
func getWholeWorkSessionTimeline(ac *client.AlpaconClient, endpoint string, params map[string]string) ([]TimelineItem, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(endpoint, "", params))
	if err != nil {
		return nil, err
	}
	var response struct {
		Results []TimelineItem  `json:"results"`
		Next    json.RawMessage `json:"next"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	// A null `next` counts: it is a paginator's last page.
	if len(response.Next) > 0 {
		return nil, ErrTimelinePaginated
	}
	return response.Results, nil
}
