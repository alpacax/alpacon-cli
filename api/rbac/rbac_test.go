package rbac

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
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
	      "role": {"id": "r2", "name": "server:owner"},
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

func TestResolveRole_ByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		count   int
		wantErr bool
	}{
		{"found", 1, false},
		{"not found or not visible", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotName string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotName = r.URL.Query().Get("name")

				var results []RoleResponse
				if tt.count > 0 {
					results = append(results, RoleResponse{ID: adminRoleID, Name: "admin"})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[RoleResponse]{Count: tt.count, Results: results})
			}))
			defer ts.Close()

			role, err := ResolveRole(newTestClient(ts), "admin")
			assert.Equal(t, "admin", gotName)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "case-sensitive")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, adminRoleID, role.ID)
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

		response := api.ListResponse[RoleAuditLogResponse]{Count: 3, Current: 1, Next: 2, Last: 2}
		response.Results = []RoleAuditLogResponse{
			{ID: "a1", Action: "granted", RoleName: "admin", Scope: "global"},
			{ID: "a2", Action: "revoked", RoleName: "admin", Scope: "global"},
			{ID: "a3", Action: "granted", RoleName: "operator", Scope: "global"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	entries, err := GetRoleHistory(newTestClient(ts), userID, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	assert.Equal(t, userID, query.Get("user"))
	assert.NotContains(t, query, "ordering")
}

// changed_by is null when the actor was a token or an application.
func TestAuditAttributesFrom_ToleratesMissingActorAndRole(t *testing.T) {
	t.Parallel()
	rows := AuditAttributesFrom([]RoleAuditLogResponse{
		{Action: "granted", RoleName: "admin", Scope: "global", ChangedBy: nil, Reason: ""},
		{Action: "revoked", RoleName: "operator", Scope: "global", ChangedBy: &AuditActor{Label: "Jane Doe"}, Reason: "rotated off"},
	})

	require.Len(t, rows, 2)
	assert.Empty(t, rows[0].ChangedBy)
	assert.Equal(t, "admin", rows[0].Role)
	assert.Equal(t, "Jane Doe", rows[1].ChangedBy)
	assert.Equal(t, "rotated off", rows[1].Reason)
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
		{Role: RoleNested{Name: "server:owner"}, ContentType: &contentType},
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

	unresolved := HolderAttributesFrom(bindings, nil)
	require.Len(t, unresolved, 1)
	assert.Equal(t, userID, unresolved[0].User)

	resolved := HolderAttributesFrom(bindings, map[string]string{userID: "jane"})
	require.Len(t, resolved, 1)
	assert.Equal(t, "jane", resolved[0].User)

	missing := HolderAttributesFrom(bindings, map[string]string{"someone-else": "bob"})
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

// Nothing else in the repo asserts these rendered timestamps: a changed timeLayout, a
// dropped .Local(), or a null added_at all render silently. The zone is pinned rather than
// read, so the UTC instant below has one correct rendering on every machine, and a render
// that skipped the conversion prints 03:04 instead.
func TestAttributesFrom_RenderTimesInTheLocalZone(t *testing.T) {
	restore := time.Local
	t.Cleanup(func() { time.Local = restore })
	time.Local = time.FixedZone("KST", 9*60*60)

	granted := time.Date(2026, 3, 4, 3, 4, 5, 0, time.UTC)
	const rendered = "2026-03-04 12:04"

	bindings := []UserRoleResponse{
		{User: types.UserSummary{ID: userID}, Role: RoleNested{Name: "admin"}, AddedAt: granted},
		{User: types.UserSummary{ID: userID}, Role: RoleNested{Name: "operator"}},
	}

	roles := BindingAttributesFrom(bindings)
	require.Len(t, roles, 2)
	assert.Equal(t, rendered, roles[0].GrantedAt)
	assert.Empty(t, roles[1].GrantedAt, "a missing added_at must not render as year 1")

	holders := HolderAttributesFrom(bindings, nil)
	require.Len(t, holders, 2)
	assert.Equal(t, rendered, holders[0].GrantedAt)
	assert.Empty(t, holders[1].GrantedAt)

	audit := AuditAttributesFrom([]RoleAuditLogResponse{
		{Action: "granted", RoleName: "admin", AddedAt: granted},
		{Action: "revoked", RoleName: "admin"},
	})
	require.Len(t, audit, 2)
	assert.Equal(t, rendered, audit[0].At)
	assert.Empty(t, audit[1].At)
}

// Both translations must match what ScopeLabel prints for the same tier, or one tier reads
// as two down the SCOPE column of 'user role ls' beside 'user role history'.
func TestTierLabel(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "workspace", tierLabel("global"))
	assert.Equal(t, "type", tierLabel("content_type"))
	assert.Equal(t, "object", tierLabel("object"))
	assert.Equal(t, "", tierLabel(""), "a scope the server omitted stays blank rather than becoming 'workspace'")
}
