package worksession

import (
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWantSelectsOnlyMatchingServerSessions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}, Exclude: "excluded"}
	tests := []struct {
		name     string
		mutate   func(*wsapi.WorkSession)
		usable   bool
		waitable bool
	}{
		{name: "active matching session", usable: true},
		{name: "pending matching session", mutate: func(ws *wsapi.WorkSession) { ws.Status = wsapi.StatusPending; ws.ExpiresAt = time.Time{} }, waitable: true},
		{name: "excluded session", mutate: func(ws *wsapi.WorkSession) { ws.ID = "excluded" }},
		{name: "agent requester", mutate: func(ws *wsapi.WorkSession) { ws.RequesterType = "agent" }},
		{name: "missing assignee", mutate: func(ws *wsapi.WorkSession) { ws.AssignedUser = nil }},
		{name: "another assignee", mutate: func(ws *wsapi.WorkSession) { ws.AssignedUser.ID = "user-2" }},
		{name: "another server", mutate: func(ws *wsapi.WorkSession) { ws.Servers[0].ID = "server-2" }},
		{name: "cluster session", mutate: func(ws *wsapi.WorkSession) { ws.Clusters = []types.ClusterSummary{{ID: "cluster-1"}} }},
		{name: "missing scope", mutate: func(ws *wsapi.WorkSession) { ws.Scopes = []string{"webftp"} }},
		{name: "expired", mutate: func(ws *wsapi.WorkSession) { ws.ExpiresAt = now.Add(-time.Second) }},
		{name: "active without expiry", mutate: func(ws *wsapi.WorkSession) { ws.ExpiresAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws := serverSession("candidate", wsapi.StatusActive, now)
			if tt.mutate != nil {
				tt.mutate(&ws)
			}

			assert.Equal(t, tt.usable, want.Usable(&ws, now))
			assert.Equal(t, tt.waitable, want.Waitable(&ws, now))
		})
	}
}

func TestOfferPickSkipsSessionStartingAfterWaitWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}
	within := serverSession("within", wsapi.StatusApproved, now)
	startsWithin := now.Add(5 * time.Minute)
	within.StartsAt = &startsWithin
	beyond := serverSession("beyond", wsapi.StatusApproved, now)
	startsBeyond := now.Add(5*time.Minute + time.Nanosecond)
	beyond.StartsAt = &startsBeyond
	beyond.AddedAt = now.Add(time.Minute)

	got, wait := pick([]wsapi.WorkSession{within, beyond}, want, now, 5*time.Minute)

	require.NotNil(t, got)
	assert.Equal(t, "within", got.ID)
	assert.True(t, wait)
	kubeUnchanged, kubeWait := Pick([]wsapi.WorkSession{beyond}, want, now)
	require.NotNil(t, kubeUnchanged)
	assert.Equal(t, "beyond", kubeUnchanged.ID)
	assert.True(t, kubeWait)
}

func TestPickPrefersUsableThenNewestWaitable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	pending := serverSession("pending-new", wsapi.StatusPending, now)
	pending.ExpiresAt = time.Time{}
	pending.AddedAt = now
	active := serverSession("active-old", wsapi.StatusActive, now)
	active.AddedAt = now.Add(-time.Hour)
	want := Want{ServerID: "server-1", AssignedUser: "user-1", RequesterType: "user", Scopes: []string{"command"}}

	got, wait := Pick([]wsapi.WorkSession{pending, active}, want, now)

	require.NotNil(t, got)
	assert.Equal(t, "active-old", got.ID)
	assert.False(t, wait)
	active.Status = wsapi.StatusExpired

	got, wait = Pick([]wsapi.WorkSession{pending, active}, want, now)

	require.NotNil(t, got)
	assert.Equal(t, "pending-new", got.ID)
	assert.True(t, wait)
}

func TestClusterWantAndPickKeepKubeBehavior(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	want := Want{ClusterID: "c-1", RequesterType: "user", Scopes: []string{"kube"}}
	active := clusterSession("active", wsapi.StatusActive, now)
	pending := clusterSession("pending", wsapi.StatusPending, now)
	pending.ExpiresAt = time.Time{}
	future := clusterSession("future", wsapi.StatusApproved, now)
	startsFuture := now.Add(time.Hour)
	future.StartsAt = &startsFuture

	assert.True(t, want.Usable(&active, now))
	assert.True(t, want.Waitable(&pending, now))
	server := serverSession("server", wsapi.StatusActive, now)
	assert.False(t, want.Usable(&server, now))
	got, wait := Pick([]wsapi.WorkSession{pending, future}, want, now)
	require.NotNil(t, got)
	assert.Equal(t, "future", got.ID)
	assert.True(t, wait)
}

func serverSession(id, status string, now time.Time) wsapi.WorkSession {
	return wsapi.WorkSession{
		ID: id, Status: status, RequesterType: "user", AssignedUser: &types.UserSummary{ID: "user-1"},
		Servers: []types.ServerSummary{{ID: "server-1"}}, Scopes: []string{"command"}, ExpiresAt: now.Add(time.Hour),
	}
}

func clusterSession(id, status string, now time.Time) wsapi.WorkSession {
	return wsapi.WorkSession{
		ID: id, Status: status, RequesterType: "user", Scopes: []string{"kube"},
		Clusters: []types.ClusterSummary{{ID: "c-1", Name: "prod-east"}}, ExpiresAt: now.Add(6 * time.Hour),
	}
}
