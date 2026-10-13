package auth0

import "encoding/json"

type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	VerificationURIComplete string `json:"verification_uri_complete"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

type Auth0Config struct {
	Method   string `json:"method"`
	ClientID string `json:"client_id,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Audience string `json:"audience,omitempty"`
	// SchemaName is the frozen workspace identity, which equals the Auth0
	// organization name. Prefer it over the workspace URL label when building
	// the org scope—the URL label can change while this never does. Empty on
	// older servers that do not return the field.
	SchemaName string `json:"schema_name,omitempty"`
}

type AuthEnvResponse struct {
	Auth0    Auth0Config `json:"auth0"`
	Language string      `json:"language"`
	Surfaces Surfaces    `json:"surfaces"`
}

// Surfaces lists the optional product surfaces the server says this workspace
// exposes. A server that predates the key leaves every surface false.
type Surfaces struct {
	Kubernetes bool
}

// UnmarshalJSON never fails. The same response feeds login, token refresh,
// revoke and logout, so a malformed surfaces value must not take those down;
// anything that is not a JSON object carrying a boolean reads as false.
func (s *Surfaces) UnmarshalJSON(data []byte) error {
	*s = Surfaces{}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	s.Kubernetes, _ = raw["kubernetes"].(bool)
	return nil
}
