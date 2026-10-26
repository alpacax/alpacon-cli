package worksession

import (
	"cmp"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSessionForSudoUpdate(t *testing.T) {
	t.Parallel()
	t.Run("pending session is rejected with actionable message", func(t *testing.T) {
		session := &wsapi.WorkSession{
			ID:     "ses-pending",
			Status: wsapi.StatusPending,
			Scopes: []string{"command", "sudo"},
		}
		err := validateSessionForSudoUpdate(session, session.Scopes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ses-pending")
		assert.Contains(t, err.Error(), "pending")
		assert.Contains(t, err.Error(), "--sudo")
	})

	t.Run("missing sudo scope is rejected with guidance", func(t *testing.T) {
		session := &wsapi.WorkSession{
			ID:     "ses-no-sudo",
			Status: "active",
			Scopes: []string{"command"},
		}
		err := validateSessionForSudoUpdate(session, session.Scopes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ses-no-sudo")
		assert.Contains(t, err.Error(), "'sudo' scope")
		// Guidance must point at the create flag, not at a separate scope flag.
		assert.Contains(t, err.Error(), "--sudo",
			"guidance should reference --sudo so the user creates the right session next time")
	})

	t.Run("active session with sudo scope passes", func(t *testing.T) {
		session := &wsapi.WorkSession{
			ID:     "ses-ok",
			Status: "active",
			Scopes: []string{"command", "sudo"},
		}
		assert.NoError(t, validateSessionForSudoUpdate(session, session.Scopes))
	})

	t.Run("approved session with sudo scope passes (pre-active is allowed)", func(t *testing.T) {
		session := &wsapi.WorkSession{
			ID:     "ses-approved",
			Status: "approved",
			Scopes: []string{"sudo"},
		}
		assert.NoError(t, validateSessionForSudoUpdate(session, session.Scopes))
	})

	t.Run("sudo scope added via --scope in the same update passes", func(t *testing.T) {
		session := &wsapi.WorkSession{
			ID:     "ses-add-scope",
			Status: "active",
			Scopes: []string{"command"},
		}
		// req.Scopes replaces the list and adds 'sudo', so the effective scopes include it.
		assert.NoError(t, validateSessionForSudoUpdate(session, []string{"command", "sudo"}))
	})
}

func TestApplyWorkSessionUpdate_PreservesExistingSudoPolicies(t *testing.T) {
	t.Parallel()
	var gotPATCH wsapi.WorkSessionUpdateRequest
	patchCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wsapi.WorkSession{
				ID:     "ses-1",
				Status: "active",
				Scopes: []string{"sudo"},
				SudoPolicies: []wsapi.SudoPolicyInline{
					{ID: "pol-old", Commands: []string{"systemctl restart nginx"}, SessionAuthorized: true},
				},
			})
		case http.MethodPatch:
			patchCalled = true
			assert.Equal(t, "/api/work-sessions/sessions/ses-1/", r.URL.Path)
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotPATCH))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wsapi.WorkSession{ID: "ses-1", Status: "active"})
		default:
			assert.Failf(t, "unexpected method", "%s", r.Method)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := applyWorkSessionUpdate(ac, "ses-1", wsapi.WorkSessionUpdateRequest{}, []wsapi.SudoPolicyInline{
		{Commands: []string{"systemctl reload nginx"}, SessionAuthorized: true},
	})
	require.NoError(t, err)
	assert.True(t, patchCalled, "PATCH must be issued")
	assert.Len(t, gotPATCH.SudoPolicies, 2)
	assert.Equal(t, "pol-old", gotPATCH.SudoPolicies[0].ID)
	assert.Empty(t, gotPATCH.SudoPolicies[1].ID)
	assert.Equal(t, []string{"systemctl reload nginx"}, gotPATCH.SudoPolicies[1].Commands)
}

func TestApplyWorkSessionUpdate_PendingSessionAbortsBeforePATCH(t *testing.T) {
	t.Parallel()
	patchCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchCalled = true
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wsapi.WorkSession{
			ID:     "ses-pending",
			Status: "pending",
			Scopes: []string{"sudo"},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := applyWorkSessionUpdate(ac, "ses-pending", wsapi.WorkSessionUpdateRequest{},
		[]wsapi.SudoPolicyInline{{Commands: []string{"x"}, SessionAuthorized: true}})
	require.Error(t, err)
	assert.False(t, patchCalled, "PATCH must not be issued when the validator rejects")
}

func TestApplyWorkSessionUpdate_FieldsOnlySkipsGet(t *testing.T) {
	t.Parallel()
	var raw map[string]any
	getCalled, patchCalled := false, false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalled = true
		case http.MethodPatch:
			patchCalled = true
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&raw))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wsapi.WorkSession{ID: "ses-1", Status: "pending"})
		default:
			assert.Failf(t, "unexpected method", "%s", r.Method)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := applyWorkSessionUpdate(ac, "ses-1", wsapi.WorkSessionUpdateRequest{
		Title:   "deploy v2",
		Servers: []string{"srv-1", "srv-2"},
	}, nil)
	require.NoError(t, err)
	assert.False(t, getCalled, "no GET should be issued when sudo is not touched")
	assert.True(t, patchCalled)
	assert.Equal(t, "deploy v2", raw["title"])
	assert.Equal(t, []any{"srv-1", "srv-2"}, raw["servers"])
	_, hasDesc := raw["description"]
	assert.False(t, hasDesc, "unset description must be omitted")
	_, hasSudo := raw["sudo_policies"]
	assert.False(t, hasSudo, "unset sudo_policies must be omitted so existing policies are kept")
	_, hasScopes := raw["scopes"]
	assert.False(t, hasScopes, "unset scopes must be omitted")
}

func TestApplyWorkSessionUpdate_TimeFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		status       string
		patchStatus  string // status the PATCH answers with; empty means unchanged since the GET
		storedStart  string // start the PATCH answers with while still pending; empty means the one sent
		storedExpiry string // expiry the PATCH answers with while still pending; empty means the one sent
		req          wsapi.WorkSessionUpdateRequest
		wantErr      []string
		notWant      []string
		wantPatch    bool
	}{
		{
			name:    "expires-at on an active session is refused",
			status:  "active",
			req:     wsapi.WorkSessionUpdateRequest{Title: "deploy v2", ExpiresAt: "2027-01-15T12:00:00Z"},
			wantErr: []string{"ses-1", "--expires-at", "alpacon work-session extend", "--reason"},
		},
		{
			name:    "starts-at on an approved session is refused",
			status:  "approved",
			req:     wsapi.WorkSessionUpdateRequest{StartsAt: "2027-01-15T10:00:00Z"},
			wantErr: []string{"ses-1", "--starts-at", "pending"},
		},
		{
			name:    "expires-at on an expired session is refused without an extend hint",
			status:  "expired",
			req:     wsapi.WorkSessionUpdateRequest{ExpiresAt: "2027-01-15T12:00:00Z"},
			wantErr: []string{"ses-1", "--expires-at", "pending"},
			notWant: []string{"extend"},
		},
		{
			name:      "both on a pending session are sent",
			status:    "pending",
			req:       wsapi.WorkSessionUpdateRequest{StartsAt: "2027-01-15T10:00:00Z", ExpiresAt: "2027-01-15T12:00:00Z"},
			wantPatch: true,
		},
		{
			name:      "nanosecond times the server keeps to microseconds are applied",
			status:    "pending",
			req:       wsapi.WorkSessionUpdateRequest{StartsAt: "2027-01-15T10:00:00.123456789Z", ExpiresAt: "2027-01-15T12:00:00.123456789Z"},
			wantPatch: true,
		},
		{
			name:        "session approved between the check and the update is reported",
			status:      "pending",
			patchStatus: "approved",
			req:         wsapi.WorkSessionUpdateRequest{Title: "deploy v2", ExpiresAt: "2027-01-15T12:00:00Z"},
			wantErr:     []string{"ses-1", "approved", "--expires-at was not applied"},
			notWant:     []string{"--starts-at"},
			wantPatch:   true,
		},
		{
			name:        "session approved with both times sent names both flags",
			status:      "pending",
			patchStatus: "approved",
			req:         wsapi.WorkSessionUpdateRequest{StartsAt: "2027-01-15T10:00:00Z", ExpiresAt: "2027-01-15T12:00:00Z"},
			wantErr:     []string{"ses-1", "--starts-at and --expires-at were not applied"},
			wantPatch:   true,
		},
		{
			name:         "pending session whose stored expiry differs is not blamed on an approval",
			status:       "pending",
			storedExpiry: "2027-01-15T11:00:00Z",
			req:          wsapi.WorkSessionUpdateRequest{ExpiresAt: "2027-01-15T12:00:00Z"},
			wantErr:      []string{"ses-1", "still pending", "--expires-at stored as 2027-01-15T11:00:00Z"},
			notWant:      []string{"became", "--starts-at"},
			wantPatch:    true,
		},
		{
			name:        "pending session whose stored start differs names only the start",
			status:      "pending",
			storedStart: "2027-01-15T09:00:00Z",
			req:         wsapi.WorkSessionUpdateRequest{StartsAt: "2027-01-15T10:00:00Z"},
			wantErr:     []string{"ses-1", "still pending", "--starts-at stored as 2027-01-15T09:00:00Z"},
			notWant:     []string{"became", "--expires-at"},
			wantPatch:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var raw map[string]any
			patchCalled := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodGet:
					_ = json.NewEncoder(w).Encode(wsapi.WorkSession{ID: "ses-1", Status: tt.status})
				case http.MethodPatch:
					patchCalled = true
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&raw))
					resp := wsapi.WorkSession{ID: "ses-1", Status: tt.status}
					if tt.patchStatus != "" {
						resp.Status = tt.patchStatus
					}
					if resp.Status == wsapi.StatusPending { // the server drops the time fields otherwise
						if startsAt, err := time.Parse(time.RFC3339, cmp.Or(tt.storedStart, tt.req.StartsAt)); err == nil {
							startsAt = startsAt.Truncate(time.Microsecond)
							resp.StartsAt = &startsAt
						}
						if expiresAt, err := time.Parse(time.RFC3339, cmp.Or(tt.storedExpiry, tt.req.ExpiresAt)); err == nil {
							resp.ExpiresAt = expiresAt.Truncate(time.Microsecond)
						}
					}
					_ = json.NewEncoder(w).Encode(resp)
				default:
					assert.Failf(t, "unexpected method", "%s", r.Method)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			_, err := applyWorkSessionUpdate(ac, "ses-1", tt.req, nil)

			assert.Equal(t, tt.wantPatch, patchCalled)
			if len(tt.wantErr) == 0 {
				require.NoError(t, err)
				assert.Equal(t, tt.req.StartsAt, raw["starts_at"])
				assert.Equal(t, tt.req.ExpiresAt, raw["expires_at"])
				return
			}
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			for _, unwanted := range tt.notWant {
				assert.NotContains(t, err.Error(), unwanted)
			}
		})
	}
}

func TestApplyWorkSessionUpdate_SudoPlusFields(t *testing.T) {
	t.Parallel()
	var gotPATCH wsapi.WorkSessionUpdateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wsapi.WorkSession{
				ID: "ses-1", Status: "active", Scopes: []string{"sudo"},
			})
		case http.MethodPatch:
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotPATCH))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wsapi.WorkSession{ID: "ses-1", Status: "active"})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := applyWorkSessionUpdate(ac, "ses-1",
		wsapi.WorkSessionUpdateRequest{Description: "rollout"},
		[]wsapi.SudoPolicyInline{{Commands: []string{"systemctl reload nginx"}, SessionAuthorized: true}})
	require.NoError(t, err)
	assert.Equal(t, "rollout", gotPATCH.Description)
	assert.Len(t, gotPATCH.SudoPolicies, 1)
}

func TestParseRFC3339Flag(t *testing.T) {
	t.Parallel()
	v, err := parseRFC3339Flag("--starts-at", "  2027-01-15T10:00:00Z  ")
	require.NoError(t, err)
	assert.Equal(t, "2027-01-15T10:00:00Z", v)

	_, err = parseRFC3339Flag("--starts-at", "2027-01-15 10:00:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--starts-at")
	assert.Contains(t, err.Error(), "RFC3339")
}
