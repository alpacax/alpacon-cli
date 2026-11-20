package rbac

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	adminRoleID = "11111111-1111-1111-1111-111111111111"
	userID      = "22222222-2222-2222-2222-222222222222"
)

func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

func TestGetUserBindings_DecodesNestedRoleAndNullScope(t *testing.T) {
	t.Parallel()
	body := `{
	  "count": 2, "current": 1, "next": 0, "previous": 0, "last": 1,
	  "results": [
	    {
	      "id": "b1", 
	      "user": {"id": "` + userID + `", "name": "Jane Doe", "email": "jane@example.com"},
	      "role": {"id": "` + adminRoleID + `", "name": "admin"},
	      "content_type": null, "object_id": null,
	      "added_at": "2026-08-01T00:00:00Z", "updated_at": "2026-08-01T00:00:00Z"
	    },
	    {
	      "id": "b2",
	      "user": {"id": "` + userID + `", "name": "Jane Doe", "email": "jane@example.com"},
	      "role": {"id": "r2", "name": "server:maintainer"},
	      "content_type": 42, "object_id": "web-01",
	      "added_at": "2026-08-02T00:00:00Z", "updated_at": "2026-08-02T00:00:00Z"
	    }
	  ]
	}`

	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("user")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	bindings, err := GetUserBindings(newTestClient(ts), userID)
	require.NoError(t, err)
	require.Len(t, bindings, 2)

	assert.Equal(t, userID, gotQuery)
	assert.Equal(t, "admin", bindings[0].Role.Name)
	assert.Equal(t, adminRoleID, bindings[0].Role.ID)
	assert.True(t, bindings[0].IsWorkspaceWide(), "a binding with both scope columns null is workspace-wide")
	assert.Equal(t, "jane@example.com", bindings[0].User.Email)

	assert.False(t, bindings[1].IsWorkspaceWide(), "an object-scoped binding is not workspace-wide")
	require.NotNil(t, bindings[1].ContentType)
	assert.Equal(t, 42, *bindings[1].ContentType)
	require.NotNil(t, bindings[1].ObjectID)
	assert.Equal(t, "web-01", *bindings[1].ObjectID)
}

func TestScopeLabel(t *testing.T) {
	t.Parallel()
	contentType := 42
	objectID := "web-01"
	blank := ""

	tests := []struct {
		name    string
		binding UserRoleResponse
		want    string
	}{
		{"workspace-wide", UserRoleResponse{}, "workspace"},
		{"blank object id counts as unscoped", UserRoleResponse{ObjectID: &blank}, "workspace"},
		{"content-type wide", UserRoleResponse{ContentType: &contentType}, "type:42"},
		{"single object", UserRoleResponse{ContentType: &contentType, ObjectID: &objectID}, "type:42/web-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.binding.ScopeLabel())
		})
	}
}

// TestResolveRole_ByName puts the decoy first, so a dropped or wrong-key filter answers with it.
func TestResolveRole_ByName(t *testing.T) {
	t.Parallel()
	roles := []RoleResponse{
		{ID: "role-uuid-first", Name: "auditor"},
		{ID: adminRoleID, Name: "admin"},
	}

	tests := []struct {
		name     string
		roleName string
		wantID   string
		wantErr  bool
		wantCall bool
	}{
		{"found", "admin", adminRoleID, false, true},
		{"padded name", "  admin  ", adminRoleID, false, true},
		{"not found or not visible", "ghost-role", "", true, true},
		{"whitespace only name", "   ", "", true, false},
		{"empty name", "", "", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var called atomic.Bool

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				filter := strings.TrimSpace(r.URL.Query().Get("name"))

				results := roles
				if filter != "" {
					results = nil
					for _, role := range roles {
						if role.Name == filter {
							results = append(results, role)
						}
					}
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[RoleResponse]{Count: len(results), Results: results})
			}))
			defer ts.Close()

			role, err := ResolveRole(newTestClient(ts), tt.roleName)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, role)
				if strings.TrimSpace(tt.roleName) == "" {
					require.ErrorIs(t, err, ErrBlankName)
				} else {
					require.ErrorIs(t, err, ErrNotFound)
					assert.Contains(t, err.Error(), "case-sensitive")
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, role.ID)
			}
			assert.Equal(t, tt.wantCall, called.Load(), "whether the name reached the API")
		})
	}
}

func TestResolveRole_ByUUID(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RoleResponse{ID: adminRoleID, Name: "admin"})
	}))
	defer ts.Close()

	role, err := ResolveRole(newTestClient(ts), adminRoleID)
	require.NoError(t, err)
	assert.Equal(t, "/api/rbac/roles/"+adminRoleID+"/", gotPath)
	assert.Equal(t, "admin", role.Name)
}

func TestGetRoleCatalog_SendsFilterOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	hide := false

	tests := []struct {
		name         string
		autoAssigned *bool
		want         string
	}{
		{"unset sends no filter", nil, ""},
		{"hiding sends auto_assigned=false", &hide, "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			var present bool
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query().Get("auto_assigned")
				_, present = r.URL.Query()["auto_assigned"]
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[RoleResponse]{})
			}))
			defer ts.Close()

			_, err := GetRoleCatalog(newTestClient(ts), tt.autoAssigned)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.autoAssigned != nil, present)
		})
	}
}

func TestGetRoleCatalog_WalksEveryPage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}

		response := api.ListResponse[RoleResponse]{Count: 2, Current: page, Last: 2}
		response.Results = []RoleResponse{{ID: "r" + strconv.Itoa(page), Name: "role" + strconv.Itoa(page)}}
		if page < 2 {
			response.Next = page + 1
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	roles, err := GetRoleCatalog(newTestClient(ts), nil)
	require.NoError(t, err)
	assert.Len(t, roles, 2)
}

func TestGetRoleHistory_StopsAtTailAndSendsNoOrdering(t *testing.T) {
	t.Parallel()
	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":3,"current":1,"next":2,"last":2,"reach":"bound","results":[
			{"id":"a1","action":"granted","role_name":"admin","scope":"global"},
			{"id":"a2","action":"revoked","role_name":"admin","scope":"global"},
			{"id":"a3","action":"granted","role_name":"operator","scope":"global"}]}`))
	}))
	defer ts.Close()

	entries, reach, err := GetRoleHistory(newTestClient(ts), userID, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, Reach{Raw: json.RawMessage(`"bound"`), Value: ReachBound}, reach)

	assert.Equal(t, userID, query.Get("user"))
	assert.NotContains(t, query, "ordering")
}

// changed_by is null when the actor was a token or an application.
func TestAuditAttributesFrom_ToleratesMissingActorAndRole(t *testing.T) {
	t.Parallel()
	rows := AuditAttributesFrom([]RoleAuditLogResponse{
		{Action: "granted", RoleName: "admin", Scope: "global", ChangedBy: nil, Reason: ""},
		{Action: "revoked", RoleName: "operator", Scope: "global", ChangedBy: &AuditActor{Label: "Jane Doe"}, Reason: "rotated off"},
	}, time.UTC)

	require.Len(t, rows, 2)
	assert.Empty(t, rows[0].ChangedBy)
	assert.Equal(t, "admin", rows[0].Role)
	assert.Equal(t, "Jane Doe", rows[1].ChangedBy)
	assert.Equal(t, "rotated off", rows[1].Reason)
}

func TestRoleAuditLogResponse_DecodesTargetAndErasure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		wantType   *string
		wantID     *string
		wantHidden int
		wantErased bool
	}{
		{
			name:     "an object target",
			body:     `{"scope":"object","context":{"scope_content_type":"servers.server","scope_object_id":"550e8400-e29b-41d4-a716-446655440000"},"targets_hidden":0}`,
			wantType: new("servers.server"),
			wantID:   new("550e8400-e29b-41d4-a716-446655440000"),
		},
		{
			name:       "a withheld target keeps its kind and loses its id",
			body:       `{"scope":"object","context":{"scope_content_type":"servers.server"},"targets_hidden":1}`,
			wantType:   new("servers.server"),
			wantHidden: 1,
		},
		{
			name: "a server that predates targets_hidden and context",
			body: `{"scope":"global"}`,
		},
		{
			name:       "an erased row",
			body:       `{"scope":"global","context":{"erased":true}}`,
			wantErased: true,
		},
		{
			name: "erased false",
			body: `{"scope":"global","context":{"erased":false}}`,
		},
		{
			name: "erased null",
			body: `{"scope":"global","context":{"erased":null}}`,
		},
		{
			name: "erased as a value other than true",
			body: `{"scope":"global","context":{"erased":"true"}}`,
		},
		{
			name:     "a numeric object id is kept as its text",
			body:     `{"scope":"object","context":{"scope_content_type":"servers.server","scope_object_id":42}}`,
			wantType: new("servers.server"),
			wantID:   new("42"),
		},
		{
			name:     "a key of another shape reads as absent",
			body:     `{"scope":"object","context":{"scope_content_type":"servers.server","scope_object_id":{"id":"x"}}}`,
			wantType: new("servers.server"),
		},
		{
			name: "a null object id",
			body: `{"scope":"content_type","context":{"scope_object_id":null}}`,
		},
		{
			name: "a context that is not an object",
			body: `{"scope":"global","context":"servers.server"}`,
		},
		{
			name: "a null context",
			body: `{"scope":"global","context":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var entry RoleAuditLogResponse
			require.NoError(t, json.Unmarshal([]byte(tt.body), &entry))

			assert.Equal(t, tt.wantType, entry.Context.ScopeContentType)
			assert.Equal(t, tt.wantID, entry.Context.ScopeObjectID)
			assert.Equal(t, tt.wantHidden, entry.TargetsHidden)
			assert.Equal(t, tt.wantErased, entry.Context.Erased)
		})
	}
}

func TestTargetCell(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType *string
		objectID    *string
		hidden      int
		want        string
	}{
		{name: "a workspace-wide binding names no target", want: ""},
		{name: "a binding on every object of a kind", contentType: new("servers.server"), want: "servers.server"},
		// Not ScopeLabel's type:42/id: the audit row carries the kind's label.
		{name: "one object", contentType: new("servers.server"), objectID: new("550e8400-e29b-41d4-a716-446655440000"), want: "servers.server/550e8400-e29b-41d4-a716-446655440000"},
		{name: "a withheld object shows its kind and the count", contentType: new("servers.server"), hidden: 1, want: "servers.server (1 hidden)"},
		{name: "the count is printed as sent", contentType: new("iam.group"), hidden: 2, want: "iam.group (2 hidden)"},
		{name: "a withheld object with no kind still says it is hidden", hidden: 1, want: "(1 hidden)"},
		{name: "an id with no kind is shown alone", objectID: new("42"), want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, targetCell(tt.contentType, tt.objectID, tt.hidden))
		})
	}
}

func TestReasonCell(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		reason string
		hidden int
		erased bool
		want   string
	}{
		{name: "a reason is shown as given", reason: "on-call", want: "on-call"},
		{name: "a reason never given stays blank", want: ""},
		{name: "a withheld target withholds its reason", hidden: 1, want: "(withheld)"},
		{name: "an erased row says so", erased: true, want: "(erased)"},
		{name: "a reason the server sent wins over the markers", reason: "rotated off", hidden: 1, erased: true, want: "rotated off"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, reasonCell(tt.reason, tt.hidden, tt.erased))
		})
	}
}

// JSON carries the fields as sent: the table's words for a withheld reason and target stay out.
func TestAuditAttributesFrom_JSONKeepsAWithheldRowAsSent(t *testing.T) {
	t.Parallel()
	added := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	rows := AuditAttributesFrom([]RoleAuditLogResponse{{
		Action: "granted", RoleName: "server:viewer", Scope: "object",
		ChangedBy: &AuditActor{Label: "bob"}, Reason: "",
		Context:       AuditContext{ScopeContentType: new("servers.server")},
		TargetsHidden: 1, AddedAt: added,
	}}, time.UTC)

	require.Len(t, rows, 1)
	assert.Equal(t, "servers.server (1 hidden)", rows[0].Target)
	assert.Equal(t, "(withheld)", rows[0].ReasonCell)

	body, err := json.Marshal(rows)
	require.NoError(t, err)
	assert.JSONEq(t, `[{
		"action":"granted","role":"server:viewer","scope":"object",
		"target_type":"servers.server","target_id":null,"targets_hidden":1,
		"changed_by":"bob","reason":"","erased":false,"at":"2026-11-10T09:00:00Z"
	}]`, string(body))
}

func TestAuditAttributesFrom_CarriesAnErasedRow(t *testing.T) {
	t.Parallel()
	added := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	rows := AuditAttributesFrom([]RoleAuditLogResponse{{
		Action: "granted", RoleName: "operator", Scope: "global", Reason: "",
		Context: AuditContext{Erased: true}, AddedAt: added,
	}}, time.UTC)

	require.Len(t, rows, 1)
	assert.Equal(t, "(erased)", rows[0].ReasonCell)

	body, err := json.Marshal(rows)
	require.NoError(t, err)
	assert.JSONEq(t, `[{
		"action":"granted","role":"operator","scope":"workspace",
		"target_type":null,"target_id":null,"targets_hidden":0,
		"changed_by":"","reason":"","erased":true,"at":"2026-11-10T09:00:00Z"
	}]`, string(body))
}

func TestFindWorkspaceBindingIgnoresObjectScopedRows(t *testing.T) {
	t.Parallel()
	contentType := 42
	objectID := "web-01"
	bindings := []UserRoleResponse{
		{ID: "scoped", Role: RoleNested{ID: adminRoleID, Name: "admin"}, ContentType: &contentType, ObjectID: &objectID},
		{ID: "workspace", Role: RoleNested{ID: adminRoleID, Name: "admin"}},
	}

	found := FindWorkspaceBinding(bindings, adminRoleID)
	require.NotNil(t, found)
	assert.Equal(t, "workspace", found.ID)

	assert.Nil(t, FindWorkspaceBinding(bindings, "other-role"))
	assert.True(t, HoldsWorkspaceRole(bindings, "admin"), "the workspace-wide admin row must count")
	assert.False(t, HoldsWorkspaceRole(bindings, "superuser"), "an unheld role must not count")
}

func TestHoldsWorkspaceRoleIgnoresObjectScopedRows(t *testing.T) {
	t.Parallel()
	contentType := 42
	bindings := []UserRoleResponse{
		{Role: RoleNested{ID: adminRoleID, Name: "admin"}, ContentType: &contentType},
	}

	assert.False(t, HoldsWorkspaceRole(bindings, "admin"), "an object-scoped admin row is not a platform tier")
}

func TestScopeAttributesFrom_ListsWildcardsFirst(t *testing.T) {
	t.Parallel()
	rows := ScopeAttributesFrom(&RoleScopesResponse{
		Wildcards: []string{"*"},
		Resources: []RoleScopeResource{{Name: "server", Actions: []string{"read", "update"}, ACL: []string{"command"}}},
	})

	require.Len(t, rows, 2)
	assert.Equal(t, "*", rows[0].Name)
	assert.Empty(t, rows[0].Actions)
	assert.Equal(t, "server", rows[1].Name)
	assert.Equal(t, "read, update", rows[1].Actions)
	assert.Equal(t, "command", rows[1].ACL)
}

func TestGrantRole_SendsScalarsAndNoEmptyScope(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	var gotMethod string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UserRoleResponse{ID: "b1", Role: RoleNested{ID: adminRoleID, Name: "admin"}})
	}))
	defer ts.Close()

	err := GrantRole(newTestClient(ts), BindingCreateRequest{
		User:   userID,
		Role:   adminRoleID,
		Reason: "SEC-1421",
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.JSONEq(t, `{"user":"`+userID+`","role":"`+adminRoleID+`","reason":"SEC-1421"}`, string(gotBody))
}

func TestGrantRole_OmitsAnEmptyReason(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UserRoleResponse{ID: "b1"})
	}))
	defer ts.Close()

	err := GrantRole(newTestClient(ts), BindingCreateRequest{User: userID, Role: adminRoleID})
	require.NoError(t, err)
	assert.JSONEq(t, `{"user":"`+userID+`","role":"`+adminRoleID+`"}`, string(gotBody))
}

func TestRevokeRole_CarriesTheReasonInTheBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		reason   string
		wantBody string
	}{
		{"with a reason", "rotated off on-call", `{"reason":"rotated off on-call"}`},
		{"without one", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody []byte
			var gotMethod, gotPath, gotQuery string

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotQuery = r.URL.RawQuery
				gotBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer ts.Close()

			err := RevokeRole(newTestClient(ts), "b1", tt.reason)
			require.NoError(t, err)

			assert.Equal(t, http.MethodDelete, gotMethod)
			assert.Equal(t, "/api/rbac/user-roles/b1/", gotPath)
			assert.Empty(t, gotQuery, "the reason must never reach the query string")
			if tt.wantBody == "" {
				assert.Empty(t, gotBody)
				return
			}
			assert.JSONEq(t, tt.wantBody, string(gotBody))
		})
	}
}

func TestWorkspaceRoleNames_SortsAndSkipsObjectScopedRows(t *testing.T) {
	t.Parallel()
	contentType := 42
	names := WorkspaceRoleNames([]UserRoleResponse{
		{Role: RoleNested{Name: "superuser"}},
		{Role: RoleNested{Name: "server:maintainer"}, ContentType: &contentType},
		{Role: RoleNested{Name: "admin"}},
	})

	assert.Equal(t, []string{"admin", "superuser"}, names)
}

func TestIsPlatformTier(t *testing.T) {
	t.Parallel()
	assert.True(t, IsPlatformTier(RoleAdmin), "admin is a platform tier")
	assert.True(t, IsPlatformTier(RoleSuperuser), "superuser is a platform tier")
	assert.False(t, IsPlatformTier("operator"), "a capability role is not a platform tier")
}

func TestHolderAttributesFrom_NilMapPrintsIDs(t *testing.T) {
	t.Parallel()
	bindings := []UserRoleResponse{
		{User: types.UserSummary{ID: userID, Name: "Jane Doe", Email: "jane@example.com"}, Role: RoleNested{Name: "admin"}},
	}

	unresolved := HolderAttributesFrom(bindings, nil, time.UTC)
	require.Len(t, unresolved, 1)
	assert.Equal(t, userID, unresolved[0].User)

	resolved := HolderAttributesFrom(bindings, map[string]string{userID: "jane"}, time.UTC)
	require.Len(t, resolved, 1)
	assert.Equal(t, "jane", resolved[0].User)

	missing := HolderAttributesFrom(bindings, map[string]string{"someone-else": "bob"}, time.UTC)
	require.Len(t, missing, 1)
	assert.Equal(t, "Jane Doe", missing[0].User)
}

func TestGrantRole_ToleratesAnEmptyCreatedBody(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	assert.NoError(t, GrantRole(newTestClient(ts), BindingCreateRequest{User: userID, Role: adminRoleID}))
}

// A renamed field on the check endpoint would decode to false: can-i prints "no" and
// -q exits 1—fail-closed, but silently wrong.
func TestIAMHostedReadsHitTheRightPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		call     func(ac *client.AlpaconClient) error
		wantPath string
		wantQry  string
		body     string
	}{
		{
			name:     "check mode sends the permission and reads allowed",
			wantPath: "/api/iam/users/-/permissions/",
			wantQry:  "server:update",
			body:     `{"allowed": true}`,
			call: func(ac *client.AlpaconClient) error {
				allowed, err := CheckPermission(ac, "-", "server:update")
				if err == nil && !allowed {
					return errors.New("allowed decoded as false")
				}
				return err
			},
		},
		{
			name:     "list mode buckets the patterns by scope",
			wantPath: "/api/iam/users/-/permissions/",
			body:     `{"global": ["server:read"], "object_scoped": ["note:update"]}`,
			call: func(ac *client.AlpaconClient) error {
				patterns, err := GetPermissionPatterns(ac, "-")
				if err == nil && (len(patterns.Global) != 1 || len(patterns.ObjectScoped) != 1) {
					return errors.New("buckets did not decode")
				}
				return err
			},
		},
		{
			name:     "effective permissions reads the provenance",
			wantPath: "/api/iam/users/-/effective-permissions/",
			body:     `{"user":{"id":"u"},"summary":{"role_count":1},"roles":[{"role":{"name":"admin"},"source":"user","scope":"global"}],"permissions":{"resources":[],"wildcards":["*"]}}`,
			call: func(ac *client.AlpaconClient) error {
				effective, err := GetEffectivePermissions(ac, "-")
				if err == nil && (len(effective.Roles) != 1 || effective.Roles[0].Role.Name != "admin") {
					return errors.New("roles did not decode")
				}
				return err
			},
		},
		{
			name:     "role scopes is a detail action, not a query",
			wantPath: "/api/rbac/roles/" + adminRoleID + "/scopes/",
			body:     `{"resources":[{"name":"server","actions":["read"],"acl":[]}],"wildcards":[]}`,
			call: func(ac *client.AlpaconClient) error {
				scopes, err := GetRoleScopes(ac, adminRoleID)
				if err == nil && len(scopes.Resources) != 1 {
					return errors.New("resources did not decode")
				}
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotQry string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQry = r.URL.Query().Get("permission")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			require.NoError(t, tt.call(newTestClient(ts)))
			assert.Equal(t, tt.wantPath, gotPath)
			assert.Equal(t, tt.wantQry, gotQry)
		})
	}
}

// A changed timeLayout, a dropped zone conversion (03:04, not 12:04), or a null added_at would otherwise render silently.
func TestAttributesFrom_RenderTimesInTheGivenZone(t *testing.T) {
	t.Parallel()
	kst := time.FixedZone("KST", 9*60*60)

	granted := time.Date(2026, 3, 4, 3, 4, 5, 0, time.UTC)
	const rendered = "2026-03-04 12:04"

	bindings := []UserRoleResponse{
		{User: types.UserSummary{ID: userID}, Role: RoleNested{Name: "admin"}, AddedAt: granted},
		{User: types.UserSummary{ID: userID}, Role: RoleNested{Name: "operator"}},
	}

	roles := BindingAttributesFrom(bindings, kst)
	require.Len(t, roles, 2)
	assert.Equal(t, rendered, roles[0].GrantedAt.String())
	assert.Empty(t, roles[1].GrantedAt.String(), "a missing added_at must not render as year 1")

	holders := HolderAttributesFrom(bindings, nil, kst)
	require.Len(t, holders, 2)
	assert.Equal(t, rendered, holders[0].GrantedAt.String())
	assert.Empty(t, holders[1].GrantedAt.String())

	audit := AuditAttributesFrom([]RoleAuditLogResponse{
		{Action: "granted", RoleName: "admin", AddedAt: granted},
		{Action: "revoked", RoleName: "admin"},
	}, kst)
	require.Len(t, audit, 2)
	assert.Equal(t, rendered, audit[0].At.String())
	assert.Empty(t, audit[1].At.String())
}

func TestAttributesFrom_JSONKeepsUTCTimeAndNullForMissing(t *testing.T) {
	t.Parallel()
	kst := time.FixedZone("KST", 9*60*60)
	granted := time.Date(2026, 3, 4, 3, 4, 5, 0, time.UTC)
	bindings := []UserRoleResponse{
		{Role: RoleNested{Name: "admin"}, AddedAt: granted.In(kst)},
		{Role: RoleNested{Name: "operator"}},
	}

	body, err := json.Marshal(BindingAttributesFrom(bindings, kst))

	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"role":"admin","scope":"workspace","granted_at":"2026-03-04T03:04:05Z"},
		{"role":"operator","scope":"workspace","granted_at":null}
	]`, string(body))
}

// Both translations must match what ScopeLabel prints for the same tier, or one tier reads
// as two down the SCOPE column of 'user role ls' beside 'user role history'.
func TestTierLabel(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "workspace", tierLabel("global"))
	assert.Equal(t, "type", tierLabel("content_type"))
	assert.Equal(t, "object", tierLabel("object"))
	assert.Empty(t, tierLabel(""), "a scope the server omitted stays blank rather than becoming 'workspace'")
}

func TestRBACCallsRefuseAPathTraversingIDBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	const badID = "../../etc/test"
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
	}{
		{"GetEffectivePermissions", func(ac *client.AlpaconClient) error {
			_, err := GetEffectivePermissions(ac, badID)
			return err
		}},
		{"GetPermissionPatterns", func(ac *client.AlpaconClient) error {
			_, err := GetPermissionPatterns(ac, badID)
			return err
		}},
		{"CheckPermission", func(ac *client.AlpaconClient) error {
			_, err := CheckPermission(ac, badID, "server:read")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := tt.call(ac)

			require.ErrorIs(t, err, utils.ErrInvalidID)
		})
	}
}

func TestRBACCallsRefuseAnUnusableIDFromAResponseBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	const badID = ".."
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
	}{
		{"GetRoleScopes", func(ac *client.AlpaconClient) error {
			_, err := GetRoleScopes(ac, badID)
			return err
		}},
		{"RevokeRole", func(ac *client.AlpaconClient) error { return RevokeRole(ac, badID, "") }},
		{"RevokeRole_with_reason", func(ac *client.AlpaconClient) error { return RevokeRole(ac, badID, "left the team") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := tt.call(ac)

			require.ErrorIs(t, err, api.ErrUnusableID)
			assert.False(t, utils.IsUsageError(err), "err: %v", err)
		})
	}
}

func TestUserIDDashMeansSelfAndReachesTheServer(t *testing.T) {
	t.Parallel()
	var path atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := GetEffectivePermissions(ac, "-")

	require.NoError(t, err)
	assert.Equal(t, "/api/iam/users/-/effective-permissions/", path.Load())
}
