package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestValidateSessionForSudoUpdate(t *testing.T) {
	t.Run("pending session is rejected with actionable message", func(t *testing.T) {
		session := &wsapi.WorkSession{
			ID:     "ses-pending",
			Status: pendingWorkSessionStatus,
			Scopes: []string{"command", "sudo"},
		}
		err := validateSessionForSudoUpdate(session, session.Scopes)
		assert.Error(t, err)
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
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ses-no-sudo")
		assert.Contains(t, err.Error(), "'sudo' scope")
		// Guidance must point at the create flag, not at a separate scope flag.
		assert.True(t, strings.Contains(err.Error(), "--sudo"),
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
					{ID: "pol-old", Commands: []string{"systemctl restart nginx"}, AllowBypassMFA: true},
				},
			})
		case http.MethodPatch:
			patchCalled = true
			assert.Equal(t, "/api/work-sessions/sessions/ses-1/", r.URL.Path)
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotPATCH))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wsapi.WorkSession{ID: "ses-1", Status: "active"})
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := applyWorkSessionUpdate(ac, "ses-1", wsapi.WorkSessionUpdateRequest{}, []wsapi.SudoPolicyInline{
		{Commands: []string{"systemctl reload nginx"}, AllowBypassMFA: true},
	})
	assert.NoError(t, err)
	assert.True(t, patchCalled, "PATCH must be issued")
	assert.Len(t, gotPATCH.SudoPolicies, 2)
	assert.Equal(t, "pol-old", gotPATCH.SudoPolicies[0].ID)
	assert.Empty(t, gotPATCH.SudoPolicies[1].ID)
	assert.Equal(t, []string{"systemctl reload nginx"}, gotPATCH.SudoPolicies[1].Commands)
}

func TestApplyWorkSessionUpdate_PendingSessionAbortsBeforePATCH(t *testing.T) {
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
		[]wsapi.SudoPolicyInline{{Commands: []string{"x"}, AllowBypassMFA: true}})
	assert.Error(t, err)
	assert.False(t, patchCalled, "PATCH must not be issued when the validator rejects")
}

func TestApplyWorkSessionUpdate_FieldsOnlySkipsGet(t *testing.T) {
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
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := applyWorkSessionUpdate(ac, "ses-1", wsapi.WorkSessionUpdateRequest{
		Title:    "deploy v2",
		Servers:  []string{"srv-1", "srv-2"},
		StartsAt: "2027-01-15T10:00:00Z",
	}, nil)
	assert.NoError(t, err)
	assert.False(t, getCalled, "no GET should be issued when sudo is not touched")
	assert.True(t, patchCalled)
	assert.Equal(t, "deploy v2", raw["title"])
	assert.Equal(t, []any{"srv-1", "srv-2"}, raw["servers"])
	assert.Equal(t, "2027-01-15T10:00:00Z", raw["starts_at"])
	_, hasDesc := raw["description"]
	assert.False(t, hasDesc, "unset description must be omitted")
	_, hasSudo := raw["sudo_policies"]
	assert.False(t, hasSudo, "unset sudo_policies must be omitted so existing policies are kept")
	_, hasScopes := raw["scopes"]
	assert.False(t, hasScopes, "unset scopes must be omitted")
}

func TestApplyWorkSessionUpdate_SudoPlusFields(t *testing.T) {
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
		[]wsapi.SudoPolicyInline{{Commands: []string{"systemctl reload nginx"}, AllowBypassMFA: true}})
	assert.NoError(t, err)
	assert.Equal(t, "rollout", gotPATCH.Description)
	assert.Len(t, gotPATCH.SudoPolicies, 1)
}

func TestParseRFC3339Flag(t *testing.T) {
	v, err := parseRFC3339Flag("--starts-at", "  2027-01-15T10:00:00Z  ")
	assert.NoError(t, err)
	assert.Equal(t, "2027-01-15T10:00:00Z", v)

	_, err = parseRFC3339Flag("--starts-at", "2027-01-15 10:00:00")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--starts-at")
	assert.Contains(t, err.Error(), "RFC3339")
}
