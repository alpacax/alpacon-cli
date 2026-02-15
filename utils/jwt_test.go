package utils

import (
	"encoding/base64"
	"encoding/json"
	"testing"

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

func TestDecodeJWTPayload(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		expectErr   bool
		expectClaim string
		expectValue any
	}{
		{
			name: "Valid JWT with simple claims",
			token: buildTestJWT(t, map[string]any{
				"sub":   "user123",
				"email": "test@example.com",
			}),
			expectErr:   false,
			expectClaim: "sub",
			expectValue: "user123",
		},
		{
			name: "Valid JWT with nested claims",
			token: buildTestJWT(t, map[string]any{
				"https://alpacon.io/workspaces": []map[string]string{
					{"schema_name": "ws1", "region": "ap1"},
				},
			}),
			expectErr:   false,
			expectClaim: "https://alpacon.io/workspaces",
		},
		{
			name:      "Invalid JWT - only 2 parts",
			token:     "header.payload",
			expectErr: true,
		},
		{
			name:      "Invalid JWT - single string",
			token:     "notajwt",
			expectErr: true,
		},
		{
			name:      "Invalid JWT - bad base64 payload",
			token:     "header.!!!invalid!!!.signature",
			expectErr: true,
		},
		{
			name:      "Invalid JWT - payload is not JSON",
			token:     "header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".signature",
			expectErr: true,
		},
		{
			name:      "Empty token",
			token:     "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := DecodeJWTPayload(tt.token)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, claims)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, claims)
				if tt.expectClaim != "" && tt.expectValue != nil {
					assert.Equal(t, tt.expectValue, claims[tt.expectClaim])
				}
				if tt.expectClaim != "" && tt.expectValue == nil {
					assert.Contains(t, claims, tt.expectClaim)
				}
			}
		})
	}
}

func TestDecodeJWTPayload_StandardPadding(t *testing.T) {
	// Ensure payloads that need base64 padding (=, ==) still decode correctly
	claims := map[string]any{"a": "b"}
	token := buildTestJWT(t, claims)

	result, err := DecodeJWTPayload(token)
	assert.NoError(t, err)
	assert.Equal(t, "b", result["a"])
}
