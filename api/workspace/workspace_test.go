package workspace

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
)

// buildTestJWT creates a minimal JWT string with the given payload claims.
func buildTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	assert.NoError(t, err)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	signature := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + payloadEnc + "." + signature
}

func TestGetWorkspacesFromToken(t *testing.T) {
	tests := []struct {
		name      string
		claims    map[string]any
		expectErr bool
		expectLen int
	}{
		{
			name: "Single workspace",
			claims: map[string]any{
				"https://alpacon.io/workspaces": []map[string]any{
					{"schema_name": "ws1", "auth0_id": "org_abc", "region": "ap1"},
				},
			},
			expectErr: false,
			expectLen: 1,
		},
		{
			name: "Multiple workspaces",
			claims: map[string]any{
				"https://alpacon.io/workspaces": []map[string]any{
					{"schema_name": "ws1", "auth0_id": "org_abc", "region": "ap1"},
					{"schema_name": "ws2", "auth0_id": "org_def", "region": "us1"},
					{"schema_name": "ws3", "auth0_id": "org_ghi", "region": "dev"},
				},
			},
			expectErr: false,
			expectLen: 3,
		},
		{
			name: "Missing workspaces claim",
			claims: map[string]any{
				"sub": "user123",
			},
			expectErr: true,
		},
		{
			name: "Empty workspaces list",
			claims: map[string]any{
				"https://alpacon.io/workspaces": []map[string]any{},
			},
			expectErr: false,
			expectLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := buildTestJWT(t, tt.claims)
			workspaces, err := GetWorkspacesFromToken(token)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, workspaces, tt.expectLen)
			}
		})
	}
}

func TestGetWorkspacesFromToken_FieldValues(t *testing.T) {
	token := buildTestJWT(t, map[string]any{
		"https://alpacon.io/workspaces": []map[string]any{
			{"schema_name": "production", "auth0_id": "org_123", "region": "ap1"},
		},
	})

	workspaces, err := GetWorkspacesFromToken(token)
	assert.NoError(t, err)
	assert.Len(t, workspaces, 1)
	assert.Equal(t, "production", workspaces[0].SchemaName)
	assert.Equal(t, "org_123", workspaces[0].Auth0ID)
	assert.Equal(t, "ap1", workspaces[0].Region)
}

func TestGetWorkspaceList(t *testing.T) {
	token := buildTestJWT(t, map[string]any{
		"https://alpacon.io/workspaces": []map[string]any{
			{"schema_name": "ws1", "auth0_id": "org_abc", "region": "ap1"},
			{"schema_name": "ws2", "auth0_id": "org_def", "region": "us1"},
		},
	})

	tests := []struct {
		name            string
		currentWS       string
		expectCurrentAt int // index of entry with "*"
	}{
		{
			name:            "Current is first workspace",
			currentWS:       "ws1",
			expectCurrentAt: 0,
		},
		{
			name:            "Current is second workspace",
			currentWS:       "ws2",
			expectCurrentAt: 1,
		},
		{
			name:            "Current is unknown workspace",
			currentWS:       "ws-unknown",
			expectCurrentAt: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				AccessToken:   token,
				WorkspaceName: tt.currentWS,
			}

			entries, err := GetWorkspaceList(cfg)
			assert.NoError(t, err)
			assert.Len(t, entries, 2)

			for i, entry := range entries {
				if i == tt.expectCurrentAt {
					assert.Equal(t, "*", entry.Current)
				} else {
					assert.Equal(t, "", entry.Current)
				}
			}
		})
	}
}

func TestGetWorkspaceList_SingleWorkspace(t *testing.T) {
	token := buildTestJWT(t, map[string]any{
		"https://alpacon.io/workspaces": []map[string]any{
			{"schema_name": "only-ws", "auth0_id": "org_abc", "region": "ap1"},
		},
	})

	cfg := config.Config{
		AccessToken:   token,
		WorkspaceName: "only-ws",
	}

	entries, err := GetWorkspaceList(cfg)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "only-ws", entries[0].Name)
	assert.Equal(t, "ap1", entries[0].Region)
	assert.Equal(t, "*", entries[0].Current)
}

func TestValidateAndBuildWorkspaceURL(t *testing.T) {
	token := buildTestJWT(t, map[string]any{
		"https://alpacon.io/workspaces": []map[string]any{
			{"schema_name": "ws1", "auth0_id": "org_abc", "region": "ap1"},
			{"schema_name": "ws2", "auth0_id": "org_def", "region": "us1"},
			{"schema_name": "ws3", "auth0_id": "org_ghi", "region": "dev"},
		},
	})

	tests := []struct {
		name       string
		targetName string
		expectURL  string
		expectName string
		expectErr  bool
	}{
		{
			name:       "Switch to ws1 in AP region",
			targetName: "ws1",
			expectURL:  "https://ws1.ap1.alpacon.io",
			expectName: "ws1",
			expectErr:  false,
		},
		{
			name:       "Switch to ws2 in US region",
			targetName: "ws2",
			expectURL:  "https://ws2.us1.alpacon.io",
			expectName: "ws2",
			expectErr:  false,
		},
		{
			name:       "Switch to ws3 in dev",
			targetName: "ws3",
			expectURL:  "https://ws3.dev.alpacon.io",
			expectName: "ws3",
			expectErr:  false,
		},
		{
			name:       "Non-existent workspace",
			targetName: "ws-nonexistent",
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				AccessToken:   token,
				BaseDomain:    "alpacon.io",
				WorkspaceName: "ws1",
			}

			newURL, newName, err := ValidateAndBuildWorkspaceURL(cfg, tt.targetName)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Empty(t, newURL)
				assert.Empty(t, newName)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectURL, newURL)
				assert.Equal(t, tt.expectName, newName)
			}
		})
	}
}

func TestValidateAndBuildWorkspaceURL_InvalidToken(t *testing.T) {
	cfg := config.Config{
		AccessToken: "not-a-jwt",
		BaseDomain:  "alpacon.io",
	}

	_, _, err := ValidateAndBuildWorkspaceURL(cfg, "ws1")
	assert.Error(t, err)
}
