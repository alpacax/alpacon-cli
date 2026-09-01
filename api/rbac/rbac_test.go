package rbac

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

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

// The binding serializer replaces the role primary key with a nested {id, name}
// object, and leaves both scope columns null on a workspace-wide binding. A struct
// that typed either as a plain string would fail to decode the real response.
func TestGetUserBindings_DecodesNestedRoleAndNullScope(t *testing.T) {
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

// The name filter is exact and case-sensitive, and a role the caller cannot see
// comes back as an empty page rather than a 403, so both readings share one error.
func TestResolveRole_ByName(t *testing.T) {
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

// A UUID skips the list entirely: the detail route answers a bare object, not a
// paginated envelope.
func TestResolveRole_ByUUID(t *testing.T) {
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

// auto_assigned=true would return only the object-scoped plumbing roles, so the
// filter is sent to hide them and never otherwise.
func TestGetRoleCatalog_SendsFilterOnlyWhenAsked(t *testing.T) {
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

// The server caps page_size at 100 and there are 22 seeded roles against a default
// page of 15, so the catalog has to be walked rather than read once.
func TestGetRoleCatalog_WalksEveryPage(t *testing.T) {
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
	// The view pins its own ordering and exposes no ordering_fields, so sending one
	// would be dead weight the reader would have to explain.
	assert.NotContains(t, query, "ordering")
}

// changed_by is null for a token or application actor, and the role FK is cleared
// when the role itself is deleted; the projection has to survive both.
func TestAuditAttributesFrom_ToleratesMissingActorAndRole(t *testing.T) {
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

// Only the workspace-wide row decides admin and superuser status, and the server
// offers no isnull filter, so the selection happens here.
func TestFindWorkspaceBindingIgnoresObjectScopedRows(t *testing.T) {
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
	contentType := 42
	bindings := []UserRoleResponse{
		{Role: RoleNested{ID: adminRoleID, Name: "admin"}, ContentType: &contentType},
	}

	assert.False(t, HoldsWorkspaceRole(bindings, "admin"), "an object-scoped admin row is not a platform tier")
}

func TestScopeAttributesFrom_ListsWildcardsFirst(t *testing.T) {
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

// The single-row path is the whole reason grant takes one user and one role: a list
// of more than one value anywhere flips the server into a bulk create that answers
// 201 with an empty body whether or not it wrote a thing. One JSONEq over the whole
// body pins the shape, because a per-field assert would not notice user or role
// turning into an array.
func TestGrantRole_SendsScalarsAndNoEmptyScope(t *testing.T) {
	var gotBody []byte
	var gotMethod string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UserRoleResponse{ID: "b1", Role: RoleNested{ID: adminRoleID, Name: "admin"}})
	}))
	defer ts.Close()

	binding, err := GrantRole(newTestClient(ts), BindingCreateRequest{
		User:   userID,
		Role:   adminRoleID,
		Reason: "SEC-1421",
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.JSONEq(t, `{"user":"`+userID+`","role":"`+adminRoleID+`","reason":"SEC-1421"}`, string(gotBody))
	assert.Equal(t, "admin", binding.Role.Name)
}

// An omitted reason must not put a blank one on the wire, so the server records the
// omission rather than an empty justification.
func TestGrantRole_OmitsAnEmptyReason(t *testing.T) {
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UserRoleResponse{ID: "b1"})
	}))
	defer ts.Close()

	_, err := GrantRole(newTestClient(ts), BindingCreateRequest{User: userID, Role: adminRoleID})
	require.NoError(t, err)
	assert.JSONEq(t, `{"user":"`+userID+`","role":"`+adminRoleID+`"}`, string(gotBody))
}

// The justification rides the DELETE body rather than a query parameter so it stays
// out of access logs; a revoke without one sends no body at all.
func TestRevokeRole_CarriesTheReasonInTheBody(t *testing.T) {
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
	contentType := 42
	names := WorkspaceRoleNames([]UserRoleResponse{
		{Role: RoleNested{Name: "superuser"}},
		{Role: RoleNested{Name: "server:owner"}, ContentType: &contentType},
		{Role: RoleNested{Name: "admin"}},
	})

	assert.Equal(t, []string{"admin", "superuser"}, names)
}

func TestIsPlatformTier(t *testing.T) {
	assert.True(t, IsPlatformTier(RoleAdmin), "admin is a platform tier")
	assert.True(t, IsPlatformTier(RoleSuperuser), "superuser is a platform tier")
	assert.False(t, IsPlatformTier("operator"), "a capability role is not a platform tier")
}

// The flag that skips name resolution has to produce identifiers another command
// accepts. Falling back to the nested display name there would print something
// 'user role revoke' cannot take.
func TestHolderAttributesFrom_NilMapPrintsIDs(t *testing.T) {
	bindings := []UserRoleResponse{
		{User: types.UserSummary{ID: userID, Name: "Jane Doe", Email: "jane@example.com"}, Role: RoleNested{Name: "admin"}},
	}

	unresolved := HolderAttributesFrom(bindings, nil)
	require.Len(t, unresolved, 1)
	assert.Equal(t, userID, unresolved[0].User)

	resolved := HolderAttributesFrom(bindings, map[string]string{userID: "jane"})
	require.Len(t, resolved, 1)
	assert.Equal(t, "jane", resolved[0].User)

	// A holder the map does not carry keeps the display name: the lookup ran, so the
	// absence is about that row rather than about the caller's intent.
	missing := HolderAttributesFrom(bindings, map[string]string{"someone-else": "bob"})
	require.Len(t, missing, 1)
	assert.Equal(t, "Jane Doe", missing[0].User)
}
