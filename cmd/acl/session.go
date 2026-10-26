package acl

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
)

// ledgerSession is what every acl subcommand works through: a client for the
// signer holding the signer token, and the workspace the ledger is asked about.
type ledgerSession struct {
	signer       *client.AlpaconClient
	token        string
	fresh        bool // the token was issued by this command, not read from the cache
	workspaceID  string
	workspaceURL string
	workspace    *client.AlpaconClient
}

// Seams for the parts of opening a session that reach outside this process.
var (
	loadConfig         = config.LoadConfig
	fetchAuthEnv       = auth0.FetchAuthEnv
	newWorkspaceClient = client.NewAlpaconAPIClient
	authorizeSigner    = runSignerDeviceCode
	now                = time.Now
)

// openLedger opens a ledger session or exits with the reason it cannot.
func openLedger() *ledgerSession {
	session, err := newLedgerSession()
	if err != nil {
		utils.CliErrorWithExit("%s", sentence(upperFirst(err.Error())))
	}
	return session
}

// newLedgerSession checks that this workspace keeps its command ACLs on the
// ledger, authorizes the signer audience when no usable signer token is cached,
// and confirms the account is a Command ACL admin of this workspace before any
// command runs, so a refusal names its cause instead of arriving as a bare 403.
func newLedgerSession() (*ledgerSession, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, errors.New("not logged in. Run 'alpacon login' first")
	}
	if cfg.AccessToken == "" {
		return nil, errors.New("managing pre-approved commands needs your own sign-in, not an API token. Run 'alpacon login' without -t to sign in through the browser")
	}

	envInfo, err := fetchAuthEnv(cfg.WorkspaceURL, httpclient.New(cfg.Insecure))
	if err != nil {
		return nil, fmt.Errorf("could not read %s's settings: %w", cfg.WorkspaceURL, err)
	}
	if reason := ledgerUnavailableReason(envInfo); reason != "" {
		return nil, errors.New(reason)
	}
	workspaceID := envInfo.Auth0.WorkspaceID

	target := config.SignerTarget{URL: client.SignerURL(), Audience: client.SignerAudience()}
	if err = client.CheckSignerURL(target.URL); err != nil {
		return nil, err
	}

	token, cached := cfg.SignerToken(now(), target)
	if !cached {
		token, err = authorizeSigner(cfg, envInfo, target)
		if err != nil {
			return nil, err
		}
	}

	session := &ledgerSession{
		signer:       client.NewSignerClient(token),
		token:        token,
		fresh:        !cached,
		workspaceID:  workspaceID,
		workspaceURL: cfg.WorkspaceURL,
	}

	capability, err := acl.GetCapability(session.signer)
	if err != nil {
		session.dropRefusedToken(err)
		return nil, errors.New(session.errorMessage("check your Command ACL admin role", err))
	}
	if !capability.Administers(workspaceID) {
		return nil, errors.New("your account is not a Command ACL admin of this workspace. Ask a Command ACL admin to grant you the role, under Pre-approved commands in the web console or with 'alpacon acl admin grant'")
	}
	return session, nil
}

// ledgerUnavailableReason says why this workspace's ledger cannot be managed
// from here, or "" when it can.
func ledgerUnavailableReason(envInfo *auth0.AuthEnvResponse) string {
	if envInfo.CommandACLSurface != auth0.CommandACLSurfaceAI {
		return "this workspace does not use the command pre-approval ledger. Its command ACLs are managed with 'alpacon token acl command'"
	}
	if envInfo.Auth0.Method != "auth0" || envInfo.Auth0.WorkspaceID == "" {
		return "this workspace does not offer the browser sign-in the command pre-approval ledger needs, or its server is too old to name the workspace to the ledger"
	}
	return ""
}

// runSignerDeviceCode runs the second device-code authorization, for the
// signer's audience, and caches the token it returns. The signer's API grants
// no refresh token, so this authorization lasts as long as the access token and
// is asked for again once it expires.
func runSignerDeviceCode(cfg config.Config, envInfo *auth0.AuthEnvResponse, target config.SignerTarget) (string, error) {
	utils.CliInfo("Pre-approved commands are kept by the command signer, which needs its own authorization. It lasts until its token expires and is not renewed in the background.")

	// Certificates are verified whatever the workspace login chose: --insecure
	// covers the workspace's host, not this authorization.
	deviceCode, err := auth0.RequestSignerDeviceCode(cfg.WorkspaceIdentity(), httpclient.New(false), envInfo, target.Audience)
	if err != nil {
		return "", fmt.Errorf("could not start the authorization for the command pre-approval ledger: %w", err)
	}

	uri := utils.PrintDeviceCodePrompt(os.Stderr, deviceCode.VerificationURIComplete, deviceCode.UserCode)
	utils.OpenBrowser(uri)

	tokenRes, err := auth0.PollForToken(deviceCode, envInfo)
	if err != nil {
		return "", errors.New(authorizationFailureMessage(err))
	}

	return cacheSignerToken(cfg, target, tokenRes.AccessToken, tokenRes.ExpiresIn)
}

// cacheSignerToken stores a freshly authorized signer token and hands it back,
// unless the config no longer holds the login and workspace cfg named. A
// config another shell's logout removed counts as a changed login.
func cacheSignerToken(cfg config.Config, target config.SignerTarget, token string, expiresIn int) (string, error) {
	err := config.SaveSignerToken(cfg, target, token, expiresIn)
	switch {
	case errors.Is(err, config.ErrLoginChanged), errors.Is(err, config.ErrWorkspaceChanged), errors.Is(err, fs.ErrNotExist):
		return "", errors.New("another login, logout or workspace switch changed the configuration while you were authorizing. Run the command again")
	case err != nil:
		utils.CliWarning("Could not save the authorization, so the next acl command will ask for it again: %s", err)
	}
	return token, nil
}

// authorizationFailureMessage words a device-code failure for the ledger,
// whose remedy is running the same command again rather than 'alpacon login'.
func authorizationFailureMessage(err error) string {
	switch {
	case errors.Is(err, auth0.ErrDeviceCodeDenied):
		return "authorization for the command pre-approval ledger was declined in the browser. Run the command again to retry"
	case errors.Is(err, auth0.ErrDeviceCodeTimedOut), errors.Is(err, auth0.ErrDeviceCodeExpired):
		return "authorization for the command pre-approval ledger timed out before it was confirmed in the browser. Run the command again to retry"
	default:
		return fmt.Sprintf("authorization for the command pre-approval ledger failed: %s", err)
	}
}

// workspaceAPI returns a client for the workspace this session was opened for,
// which resolves the names the ledger stores as ids. It is built once and
// refused when the config has moved to another workspace since the session
// opened, so a name is never resolved in one workspace for a ledger call
// filed under another.
func (s *ledgerSession) workspaceAPI() (*client.AlpaconClient, error) {
	if s.workspace != nil {
		return s.workspace, nil
	}
	ac, err := newWorkspaceClient()
	if err != nil {
		return nil, fmt.Errorf("connection to Alpacon API failed: %w", err)
	}
	if ac.BaseURL != s.workspaceURL {
		return nil, errors.New("the current workspace changed while this command ran. Run the command again")
	}
	s.workspace = ac
	return ac, nil
}

// fail reports a failed ledger call and exits.
func (s *ledgerSession) fail(action string, err error) {
	s.dropRefusedToken(err)
	utils.CliErrorWithExit("%s", s.errorMessage(action, err))
}

// dropRefusedToken removes the cached signer token after the signer answered
// 401, so the next command authorizes again instead of failing the same way.
func (s *ledgerSession) dropRefusedToken(err error) {
	if utils.HTTPStatusCode(err) != http.StatusUnauthorized {
		return
	}
	if clearErr := config.ClearSignerToken(s.token); clearErr != nil {
		utils.CliDebug("could not drop the refused signer token: %v", clearErr)
	}
}

// errorMessage words a failed signer call. The signer's own refusal text is
// kept, with the one thing it cannot say added to a 401 and a 403.
func (s *ledgerSession) errorMessage(action string, err error) string {
	status := utils.HTTPStatusCode(err)
	switch status {
	case 0:
		// The URL may come from an override carrying credentials, so both it
		// and the transport error that quotes it are redacted.
		return fmt.Sprintf("Failed to %s: could not reach the command signer at %s: %s. Set %s if this workspace uses another signer.",
			action, utils.RedactURL(client.SignerURL()), utils.RedactURLError(err), client.SignerURLEnvVar)
	case http.StatusUnauthorized:
		if s.fresh {
			// Authorizing again would be refused the same way, so the remedy
			// is the configuration, not another run.
			return fmt.Sprintf("Failed to %s: the command signer at %s refused the authorization this command just obtained. Check that %s names the audience that signer accepts.",
				action, utils.RedactURL(client.SignerURL()), client.SignerAudienceEnvVar)
		}
		return fmt.Sprintf("Failed to %s: the command signer no longer accepts your authorization. Run the command again to authorize once more.", action)
	}

	message := fmt.Sprintf("Failed to %s: %s", action, sentence(err.Error()))
	if status == http.StatusForbidden {
		message += " Only a Command ACL admin of this workspace may manage pre-approved commands."
	}
	return message
}

// sentence ends detail with a period: the signer's messages are sentences that
// already carry one, the CLI's own are not.
func sentence(detail string) string {
	detail = strings.TrimSpace(detail)
	if strings.HasSuffix(detail, ".") {
		return detail
	}
	return detail + "."
}

// upperFirst capitalizes an error's first letter for display: Go error strings
// start lowercase, CLI messages do not.
func upperFirst(message string) string {
	if message == "" {
		return message
	}
	return strings.ToUpper(message[:1]) + message[1:]
}

// printWarnings relays the signer's advisories on a write, each once: a batch
// of unscoped entries would otherwise repeat the same warning per row.
func printWarnings(entries []acl.EntryResponse) {
	seen := map[string]bool{}
	for _, entry := range entries {
		for _, warning := range entry.Warnings {
			if seen[warning] {
				continue
			}
			seen[warning] = true
			utils.CliWarning("%s", warning)
		}
	}
}
