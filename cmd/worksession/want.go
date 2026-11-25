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
	return ws.Status == wsapi.StatusActive && w.fits(ws, now) && w.usableNow(ws, now)
}

func (w Want) usableNow(ws *wsapi.WorkSession, now time.Time) bool {
	return (ws.StartsAt == nil || !ws.StartsAt.After(now)) && (w.AssignedUser == "" || !ws.ExpiresAt.IsZero())
}

// Waitable reports whether ws can serve the operation after its open approval settles.
func (w Want) Waitable(ws *wsapi.WorkSession, now time.Time) bool {
	return awaitingApproval(ws) && w.fits(ws, now)
}

func awaitingApproval(ws *wsapi.WorkSession) bool {
	return ws.Status == wsapi.StatusPending || ws.Status == wsapi.StatusApproved
}

// Pick prefers the newest usable session, then the newest session still waiting on approval.
func Pick(sessions []wsapi.WorkSession, want Want, now time.Time) (ws *wsapi.WorkSession, wait bool) {
	return pick(sessions, want, now, 0)
}

// pick checks each session's status before its targets and scopes, and fits each at most once.
// A positive waitLimit skips a waiting session that cannot start within it.
func pick(sessions []wsapi.WorkSession, want Want, now time.Time, waitLimit time.Duration) (ws *wsapi.WorkSession, wait bool) {
	var usable, waiting *wsapi.WorkSession
	for i := range sessions {
		candidate := &sessions[i]
		switch {
		case candidate.Status == wsapi.StatusActive:
			if newer(candidate, usable) && want.fits(candidate, now) && want.usableNow(candidate, now) {
				usable = candidate
			}
		case awaitingApproval(candidate):
			startsInTime := waitLimit <= 0 || candidate.StartsAt == nil || !candidate.StartsAt.After(now.Add(waitLimit))
			if startsInTime && newer(candidate, waiting) && want.fits(candidate, now) {
				waiting = candidate
			}
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
