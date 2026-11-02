package worksession

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const workSessionURL = "/api/work-sessions/sessions/"

// ErrTimelinePaginated is returned when a whole-timeline read is answered with
// a `next`, so a page is never shown as the whole session.
var ErrTimelinePaginated = errors.New(
	"this server paginates the work session timeline, which this version of alpacon reads only whole, " +
		"so it would show the first page as the whole session; run 'alpacon update' to install a version " +
		"that reads the timeline page by page and its recordings through the per-session recording route",
)

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

// ProjectAttributes converts a WorkSession into its table row.
func ProjectAttributes(ws *WorkSession) WorkSessionAttributes {
	return WorkSessionAttributes{
		ID:          ws.ID,
		Description: utils.TruncateString(ws.Description, 70),
		Status:      ws.Status,
		Scopes:      strings.Join(ws.Scopes, ", "),
		Servers:     strings.Join(ServerNames(ws), ", "),
		Clusters:    strings.Join(ClusterNames(ws), ", "),
		ExpiresAt:   FormatExpiresAt(ws.ExpiresAt),
	}
}

// FormatExpiresAt leaves blank the expiry of a session created with a
// duration, which has none until it activates.
func FormatExpiresAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// ServerNames lists the names of the servers ws targets.
func ServerNames(ws *WorkSession) []string {
	names := make([]string, len(ws.Servers))
	for i, srv := range ws.Servers {
		names[i] = srv.Name
	}
	return names
}

// ClusterNames lists the names of the clusters ws targets.
func ClusterNames(ws *WorkSession) []string {
	names := make([]string, len(ws.Clusters))
	for i, c := range ws.Clusters {
		names[i] = c.Name
	}
	return names
}

// TargetsCluster reports whether ws names clusterID among its targets.
func TargetsCluster(ws *WorkSession, clusterID string) bool {
	for _, c := range ws.Clusters {
		if strings.EqualFold(c.ID, clusterID) {
			return true
		}
	}
	return false
}

// ClusterSessionFilter narrows GetClusterWorkSessions. Empty fields are not
// sent; Status takes several statuses separated by commas.
type ClusterSessionFilter struct {
	ClusterID     string
	Status        string
	RequesterType string
	AssignedUser  string
}

// GetClusterWorkSessions returns, whole, the work sessions that target one
// cluster, through the server's cluster filter.
func GetClusterWorkSessions(ac *client.AlpaconClient, filter ClusterSessionFilter) ([]WorkSession, error) {
	if strings.TrimSpace(filter.ClusterID) == "" {
		return nil, errors.New("cluster ID is required")
	}
	params := map[string]string{"cluster": filter.ClusterID}
	if filter.Status != "" {
		params["status"] = filter.Status
	}
	if filter.RequesterType != "" {
		params["requester_type"] = filter.RequesterType
	}
	if filter.AssignedUser != "" {
		params["assigned_user"] = filter.AssignedUser
	}
	sessions, err := api.FetchAllPages[WorkSession](ac, workSessionURL, params)
	if err != nil {
		return nil, err
	}
	// A listed ID goes on to a read or an approval wait, so it is held to what
	// a server-sent ID must be before any of it reaches a path.
	for i := range sessions {
		pathID, err := api.RequireResponseID(sessions[i].ID, "work session")
		if err != nil {
			return nil, err
		}
		sessions[i].ID = pathID.String()
	}
	return sessions, nil
}

// FormatDuration renders d the way the server reads a duration, HH:MM:SS
// with the hours left unbounded, rounding up to the whole second so a
// sub-second remainder is never dropped to zero.
func FormatDuration(d time.Duration) string {
	seconds := int64((d + time.Second - 1) / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
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

// UpdateWorkSession PATCHes a work session. The server may queue the change for approval, in
// which case it takes effect only once approved.
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

// ExtendWorkSession requests a new expires_at for a running session and returns the HTTP status:
// 200 when the session is already extended, 202 when the workspace's approval policy holds the
// request and PendingExtensionRequest is populated. Branch on the status, not the field: a 200
// body can still carry a stale request, such as a lapsed one the server has not yet cleared.
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

// CancelWorkSession withdraws a pending session. Unlike the superuser-only RevokeWorkSession, the
// server restricts it to the creator or a superuser, and it rejects a session no longer pending.
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

// GetWorkSessionTimeline reads a work session's activity timeline. A request
// naming `cursor`, even empty, is paginated and its pages carry no recordings;
// one naming no `cursor` gets the whole timeline with recordings and no `next`
// key. So includeRecords picks the shape. `include_records` rides on the
// paginated read too, for a server that does not paginate this route.
func GetWorkSessionTimeline(ac *client.AlpaconClient, id string, includeRecords bool) ([]TimelineItem, error) {
	pathID, err := api.RequireID(id, "work session")
	if err != nil {
		return nil, err
	}
	endpoint := api.IDURL(workSessionURL, pathID, "timeline", nil)
	params := map[string]string{"include_records": strconv.FormatBool(includeRecords)}
	if includeRecords {
		return getWholeWorkSessionTimeline(ac, endpoint, params)
	}
	return api.FetchAllCursorPages[TimelineItem](ac, endpoint, params)
}

// getWholeWorkSessionTimeline reads the unpaginated shape in one request. Naming no `cursor` is
// what holds the server to that shape, so neither `cursor` nor `page_size` may be added here.
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

// ListTerminalSessionIDs returns the ids of a work session's Websh terminals in timeline order.
// Tunnels are Websh sessions the server lists under their own type and have no recording, so they
// are not terminals.
func ListTerminalSessionIDs(ac *client.AlpaconClient, id string) ([]string, error) {
	items, err := GetWorkSessionTimeline(ac, id, false /* includeRecords */)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, item := range items {
		if item.Type == "websh_session" && !item.IsTunnel {
			ids = append(ids, item.ID)
		}
	}
	return ids, nil
}

// GetTerminalRecording reads one Websh session's recording through the work session's records
// route, joining the chunks in order. addedAt is the first chunk's added_at, and found is false
// when the session has no recording.
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
