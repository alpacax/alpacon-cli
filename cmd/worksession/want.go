package worksession

import (
	"slices"
	"strings"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
)

// Want describes the ownership, target, and scopes a reusable WorkSession must have.
type Want struct {
	ServerID      string
	ClusterID     string
	AssignedUser  string
	RequesterType string
	Scopes        []string
	Exclude       string
}

func (w Want) fits(ws *wsapi.WorkSession, now time.Time) bool {
	strictOwner := w.AssignedUser != ""
	requesterMismatch := (strictOwner && ws.RequesterType != w.RequesterType) || (!strictOwner && ws.RequesterType != "" && ws.RequesterType != w.RequesterType)
	if ws.ID == w.Exclude || requesterMismatch {
		return false
	}
	if strictOwner && (ws.AssignedUser == nil || !strings.EqualFold(ws.AssignedUser.ID, w.AssignedUser)) {
		return false
	}
	if w.ClusterID != "" {
		if !wsapi.TargetsCluster(ws, w.ClusterID) || !slices.Contains(ws.Scopes, ScopeKube) {
			return false
		}
	} else {
		if wsapi.IsClusterSession(ws) || !targetsServer(ws, w.ServerID) {
			return false
		}
	}
	for _, scope := range w.Scopes {
		if !slices.Contains(ws.Scopes, scope) {
			return false
		}
	}
	// A session created with a duration has no expiry until it activates.
	return !wsapi.IsEndedStatus(ws.Status) && (ws.ExpiresAt.IsZero() || ws.ExpiresAt.After(now))
}

func targetsServer(ws *wsapi.WorkSession, id string) bool {
	for _, server := range ws.Servers {
		if strings.EqualFold(server.ID, id) {
			return true
		}
	}
	return false
}

// Usable reports whether ws can serve the wanted operation now.
func (w Want) Usable(ws *wsapi.WorkSession, now time.Time) bool {
	if !w.fits(ws, now) || ws.Status != wsapi.StatusActive || ws.StartsAt != nil && ws.StartsAt.After(now) {
		return false
	}
	return w.AssignedUser == "" || !ws.ExpiresAt.IsZero()
}

// Waitable reports whether ws can serve the operation after its open approval settles.
func (w Want) Waitable(ws *wsapi.WorkSession, now time.Time) bool {
	return w.fits(ws, now) && (ws.Status == wsapi.StatusPending || ws.Status == wsapi.StatusApproved)
}

func (w Want) waitableWithin(ws *wsapi.WorkSession, now time.Time, limit time.Duration) bool {
	return w.Waitable(ws, now) && (ws.StartsAt == nil || !ws.StartsAt.After(now.Add(limit)))
}

// Pick prefers the newest usable session, then the newest session still waiting on approval.
func Pick(sessions []wsapi.WorkSession, want Want, now time.Time) (ws *wsapi.WorkSession, wait bool) {
	return pick(sessions, want, now, 0)
}

func pick(sessions []wsapi.WorkSession, want Want, now time.Time, waitLimit time.Duration) (ws *wsapi.WorkSession, wait bool) {
	var usable, waiting *wsapi.WorkSession
	for i := range sessions {
		candidate := &sessions[i]
		if want.Usable(candidate, now) && newer(candidate, usable) {
			usable = candidate
		}
		waitable := want.Waitable(candidate, now)
		if waitLimit > 0 {
			waitable = want.waitableWithin(candidate, now, waitLimit)
		}
		if waitable && newer(candidate, waiting) {
			waiting = candidate
		}
	}
	if usable != nil {
		return usable, false
	}
	return waiting, waiting != nil
}

func newer(candidate, current *wsapi.WorkSession) bool {
	return current == nil || candidate.AddedAt.After(current.AddedAt) || candidate.AddedAt.Equal(current.AddedAt) && candidate.ID < current.ID
}
