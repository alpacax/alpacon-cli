package config

import "time"

// signerTokenExpiryMargin keeps a token about to expire from being sent: a
// request that leaves with seconds to spare can arrive expired.
const signerTokenExpiryMargin = time.Minute

// SignerTarget is the signer a token is sent to and the audience it is issued
// for. A cached token is used only with the target it was issued for.
type SignerTarget struct {
	URL      string `json:"url"`
	Audience string `json:"audience"`
}

// SignerGrant is a cached signer token. It has no refresh token, so it is
// authorized again once it expires.
type SignerGrant struct {
	AccessToken  string       `json:"access_token"`
	ExpiresAt    string       `json:"expires_at"`
	WorkspaceURL string       `json:"workspace_url"`
	Target       SignerTarget `json:"target"`
}

// SignerToken returns the cached signer token when it was issued for the
// workspace the config names and for target, and is outside its expiry margin.
func (c Config) SignerToken(now time.Time, target SignerTarget) (string, bool) {
	grant := c.Signer
	if grant == nil || grant.AccessToken == "" || grant.WorkspaceURL != c.WorkspaceURL || grant.Target != target {
		return "", false
	}
	expiresAt, err := time.Parse(time.RFC3339, grant.ExpiresAt)
	if err != nil || !now.Add(signerTokenExpiryMargin).Before(expiresAt) {
		return "", false
	}
	return grant.AccessToken, true
}

// SaveSignerToken caches accessToken for login's workspace and target. It writes
// nothing and returns ErrLoginChanged or ErrWorkspaceChanged once login is replaced.
func SaveSignerToken(login Config, target SignerTarget, accessToken string, expiresIn int) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if cfg.WorkspaceURL != login.WorkspaceURL {
			return false, ErrWorkspaceChanged
		}
		if !cfg.sameLogin(login) {
			return false, ErrLoginChanged
		}
		cfg.Signer = &SignerGrant{
			AccessToken:  accessToken,
			ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
			WorkspaceURL: login.WorkspaceURL,
			Target:       target,
		}
		return true, nil
	})
}

// ClearSignerToken drops the cached signer token while it is still accessToken,
// the one the signer refused; a token another shell cached since is kept.
func ClearSignerToken(accessToken string) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if accessToken == "" || cfg.Signer == nil || cfg.Signer.AccessToken != accessToken {
			return false, nil
		}
		cfg.Signer = nil
		return true, nil
	})
}
