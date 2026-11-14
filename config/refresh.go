package config

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

const (
	RefreshGrantTimeout = 15 * time.Second
	refreshLockTimeout  = 30 * time.Second
	refreshWaitTimeout  = refreshLockTimeout + RefreshGrantTimeout + time.Second
)

var refreshes = struct {
	sync.Mutex
	pending map[refreshKey]*refreshFlight
}{pending: make(map[refreshKey]*refreshFlight)}

type refreshKey struct{ dir, workspace, login, access, refresh, expiry string }
type refreshFlight struct {
	done  chan struct{}
	token RenewedToken
	err   error
}

// SameLogin reports whether two snapshots belong to the same login, across rotations.
func (c Config) SameLogin(other Config) bool { return c.sameLogin(other) }

// auth0LoginID also gives older configs a stable identity before their first rotation.
// A new login always writes a fresh random ID, including a login by the same account.
func (c Config) auth0LoginID() string {
	if c.Auth0LoginID != "" {
		return c.Auth0LoginID
	}
	if c.RefreshToken == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(c.RefreshToken)))
}

// RefreshAuth0Token holds the config writer lock across re-read, grant and atomic save.
// grant must not call a config writer: the lock is deliberately not reentrant.
func RefreshAuth0Token(started Config, grant func(context.Context, string) (RenewedToken, error)) (RenewedToken, error) {
	finish := utils.BeginTokenRefresh()
	defer finish()
	dir, err := configDir()
	if err != nil {
		return RenewedToken{}, err
	}
	key := refreshKey{dir, started.WorkspaceURL, started.auth0LoginID(), started.AccessToken, started.RefreshToken, started.AccessTokenExpiresAt}
	refreshes.Lock()
	if flight := refreshes.pending[key]; flight != nil {
		refreshes.Unlock()
		timer := time.NewTimer(refreshWaitTimeout)
		defer timer.Stop()
		select {
		case <-flight.done:
			return flight.token, flight.err
		case <-timer.C:
			return RenewedToken{}, fmt.Errorf("timed out after %s waiting for access token refresh", refreshWaitTimeout)
		}
	}
	flight := &refreshFlight{done: make(chan struct{})}
	refreshes.pending[key] = flight
	refreshes.Unlock()
	flight.token, flight.err = refreshAuth0Token(started, grant)
	refreshes.Lock()
	delete(refreshes.pending, key)
	close(flight.done)
	refreshes.Unlock()
	return flight.token, flight.err
}

func refreshAuth0Token(started Config, grant func(context.Context, string) (RenewedToken, error)) (RenewedToken, error) {
	var result RenewedToken
	err := withConfigLockTimeout(refreshLockTimeout, func() error {
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if !cfg.sameLogin(started) {
			return ErrLoginChanged
		}
		if cfg.WorkspaceURL == started.WorkspaceURL && refreshAdvanced(started, cfg) {
			result = cfg.renewedToken()
			return nil
		}
		if cfg.RefreshToken == "" {
			return fmt.Errorf("no refresh token stored; run 'alpacon login' to authenticate again")
		}
		ctx, cancel := context.WithTimeout(context.Background(), RefreshGrantTimeout)
		defer cancel()
		renewed, err := grant(ctx, cfg.RefreshToken)
		if err != nil {
			return err
		}
		cfg.Auth0LoginID = cfg.auth0LoginID()
		if cfg.WorkspaceURL == started.WorkspaceURL {
			cfg.setAccessToken(renewed.AccessToken, renewed.ExpiresIn)
		}
		if renewed.RefreshToken != "" {
			cfg.RefreshToken = renewed.RefreshToken
		}
		result = renewed
		if err := saveConfig(&cfg); err != nil {
			return err
		}
		result.RefreshToken = cfg.RefreshToken
		return nil
	})
	return result, err
}

func refreshAdvanced(before, after Config) bool {
	changed := before.RefreshToken != after.RefreshToken || before.AccessToken != after.AccessToken || before.AccessTokenExpiresAt != after.AccessTokenExpiresAt
	expiry, err := time.Parse(time.RFC3339, after.AccessTokenExpiresAt)
	return changed && after.AccessToken != "" && err == nil && time.Now().Before(expiry.Add(-10*time.Second))
}

func (c Config) renewedToken() RenewedToken {
	expiry, _ := time.Parse(time.RFC3339, c.AccessTokenExpiresAt)
	return RenewedToken{AccessToken: c.AccessToken, RefreshToken: c.RefreshToken, ExpiresIn: int(time.Until(expiry) / time.Second)}
}
