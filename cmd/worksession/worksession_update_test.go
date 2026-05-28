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
		err := validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-pending",
			Status: pendingWorkSessionStatus,
			Scopes: []string{"command", "sudo"},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ses-pending")
		assert.Contains(t, err.Error(), "pending")
		assert.Contains(t, err.Error(), "--sudo")
	})

	t.Run("missing sudo scope is rejected with guidance", func(t *testing.T) {
		err := validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-no-sudo",
			Status: "active",
			Scopes: []string{"command"},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ses-no-sudo")
		assert.Contains(t, err.Error(), "'sudo' scope")
		// Guidance must point at the create flag, not at a separate scope flag.
		assert.True(t, strings.Contains(err.Error(), "--sudo"),
			"guidance should reference --sudo so the user creates the right session next time")
	})

	t.Run("active session with sudo scope passes", func(t *testing.T) {
		assert.NoError(t, validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-ok",
			Status: "active",
			Scopes: []string{"command", "sudo"},
		}))
	})

	t.Run("approved session with sudo scope passes (pre-active is allowed)", func(t *testing.T) {
		assert.NoError(t, validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-approved",
			Status: "approved",
			Scopes: []string{"sudo"},
		}))
	})
}

// TestAttachSudoPoliciesToSession_PreservesExistingPolicies locks down the
// "don't drop existing policies" invariant of the modify endpoint: a future
// refactor that forgets to echo the current set back would silently delete it.
func TestAttachSudoPoliciesToSession_PreservesExistingPolicies(t *testing.T) {
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
	newPolicies := []wsapi.SudoPolicyInline{
		{Commands: []string{"systemctl reload nginx"}, AllowBypassMFA: true},
	}
	_, err := attachSudoPoliciesToSession(ac, "ses-1", newPolicies)
	assert.NoError(t, err)
	assert.True(t, patchCalled, "PATCH must be issued")
	// Full desired set is sent: existing policy echoed back with its ID + the new addition without one.
	assert.Len(t, gotPATCH.SudoPolicies, 2)
	assert.Equal(t, "pol-old", gotPATCH.SudoPolicies[0].ID)
	assert.Empty(t, gotPATCH.SudoPolicies[1].ID)
	assert.Equal(t, []string{"systemctl reload nginx"}, gotPATCH.SudoPolicies[1].Commands)
}

// TestAttachSudoPoliciesToSession_PendingSessionAbortsBeforePATCH ensures the
// validator runs in the wiring path: a pending session must error out without
// issuing the PATCH so the server doesn't even see the request.
func TestAttachSudoPoliciesToSession_PendingSessionAbortsBeforePATCH(t *testing.T) {
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
	_, err := attachSudoPoliciesToSession(ac, "ses-pending", []wsapi.SudoPolicyInline{
		{Commands: []string{"x"}, AllowBypassMFA: true},
	})
	assert.Error(t, err)
	assert.False(t, patchCalled, "PATCH must not be issued when the validator rejects")
}
