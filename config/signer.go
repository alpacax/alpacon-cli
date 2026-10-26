package config

import "time"

// signerTokenExpiryMargin keeps a signer token that is about to expire from
// being sent: a request that leaves with seconds to spare can arrive expired.
const signerTokenExpiryMargin = time.Minute

// SignerTarget is the signer a token is sent to and the audience it is issued
// for. A cached token is used only with the target it was authorized for, so a
// changed ALPACON_AI_URL never receives a token issued for another host.
type SignerTarget struct {
	URL      string
	Audience string
}

// SignerToken returns the cached signer access token when it was authorized for
// the workspace the config now names and for target, and has not reached its
// expiry margin.
func (c Config) SignerToken(now time.Time, target SignerTarget) (string, bool) {
	if c.SignerAccessToken == "" || c.SignerWorkspaceURL != c.WorkspaceURL ||
		c.SignerURL != target.URL || c.SignerAudience != target.Audience {
		return "", false
	}
	expiresAt, err := time.Parse(time.RFC3339, c.SignerAccessTokenExpiresAt)
	if err != nil || !now.Add(signerTokenExpiryMargin).Before(expiresAt) {
		return "", false
	}
	return c.SignerAccessToken, true
}

// SaveSignerToken caches accessToken as the signer token for login's workspace.
// Once another shell's login or switch has replaced login, it returns
// ErrLoginChanged or ErrWorkspaceChanged and writes nothing: the token was
// authorized for the workspace login named, and a new login is a new person
// as far as this file can tell.
func SaveSignerToken(login Config, target SignerTarget, accessToken string, expiresIn int) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if cfg.WorkspaceURL != login.WorkspaceURL {
			return false, ErrWorkspaceChanged
		}
		if !cfg.sameLogin(login) {
			return false, ErrLoginChanged
		}
		cfg.SignerAccessToken = accessToken
		cfg.SignerAccessTokenExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
		cfg.SignerWorkspaceURL = login.WorkspaceURL
		cfg.SignerURL = target.URL
		cfg.SignerAudience = target.Audience
		return true, nil
	})
}

// ClearSignerToken drops the cached signer token while it is still
// accessToken, the one the signer refused. A token another shell has cached
// since is left alone.
func ClearSignerToken(accessToken string) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if accessToken == "" || cfg.SignerAccessToken != accessToken {
			return false, nil
		}
		cfg.SignerAccessToken = ""
		cfg.SignerAccessTokenExpiresAt = ""
		cfg.SignerWorkspaceURL = ""
		cfg.SignerURL = ""
		cfg.SignerAudience = ""
		return true, nil
	})
}
