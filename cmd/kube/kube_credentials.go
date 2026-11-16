package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// execCredentialVersion is the ExecCredential version kubectl asks for.
const execCredentialVersion = "client.authentication.k8s.io/v1"

// retrieveTimeout bounds the check of the session behind the token, the
// access-token refresh building the client can run included, so a server
// that stops answering delays kubectl by at most this much.
const retrieveTimeout = 5 * time.Second

var (
	errNoAnswer = errors.New("no answer")
	// errCallInterrupted is a call a first Ctrl+C gave up on.
	errCallInterrupted = errors.New("interrupted")
)

func newKubeCredentialsCmd() *cobra.Command {
	var cluster string
	cmd := &cobra.Command{
		Use:   "credentials",
		Short: "Print the token kubectl uses for a cluster",
		Long: `Print a client.authentication.k8s.io/v1 ExecCredential holding the token of
the kube session 'alpacon kube login' opened for the cluster. kubectl runs this
command itself, through the context kube login writes; you do not run it.

Each run checks the session with the server. A session that has ended, or whose
work session has expired, exits 3 and says to run 'alpacon kube login' again.`,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runKubeCredentials(cmd.OutOrStdout(), strings.TrimSpace(cluster))
		}),
	}
	cmd.Flags().StringVar(&cluster, "cluster", "", "cluster whose saved kube session to use")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

// execCredential is the object kubectl reads from the command's stdout.
type execCredential struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Status     execCredentialStatus `json:"status"`
}

type execCredentialStatus struct {
	Token               string    `json:"token"`
	ExpirationTimestamp time.Time `json:"expirationTimestamp"`
}

// execInfo is what kubectl passes in KUBERNETES_EXEC_INFO when the context
// sets provideClusterInfo: the cluster the request is going to.
type execInfo struct {
	Spec struct {
		Cluster *struct {
			Server string `json:"server"`
		} `json:"cluster"`
	} `json:"spec"`
}

// clusterServer returns the server kubectl is about to call, or "" when
// kubectl passed no cluster.
func clusterServer(env string) string {
	var info execInfo
	if env == "" || json.Unmarshal([]byte(env), &info) != nil || info.Spec.Cluster == nil {
		return ""
	}
	return info.Spec.Cluster.Server
}

// errSessionGone ends the command with exit 3: the token cannot work again,
// and only a new login brings one.
var errSessionGone = errors.New("session gone")

// refusedCheck is a session check the server refused (401 or 403). The session
// may still be open, but this login cannot vouch for it, so the token is not
// served; it is errSessionGone with the refusal kept for the message.
type refusedCheck struct{ err error }

func (r *refusedCheck) Error() string   { return r.err.Error() }
func (r *refusedCheck) Unwrap() []error { return []error{errSessionGone, r.err} }

// sessionGoneMessage says why the saved token is not served and what to run.
// A new kube login fixes an ended session; it fails the same way on a refused
// check, so a 401 sends the user to alpacon login first and a 403 names the
// refusal.
func sessionGoneMessage(cluster string, err error) (string, []any) {
	relogin := fmt.Sprintf("run 'alpacon kube login %s'", shellQuote(cluster))
	var refused *refusedCheck
	if !errors.As(err, &refused) {
		return "The kube session for %s has ended; %s.", []any{cluster, relogin}
	}
	if utils.HTTPStatusCode(refused.err) == http.StatusUnauthorized {
		return "The server no longer accepts this login, so the kube session for %s cannot be checked; run 'alpacon login', then %s.", []any{cluster, relogin}
	}
	return "The server refused to check the kube session for %s, so the saved token is not used: %s. Resolve that refusal, then %s.", []any{cluster, refused.err, relogin}
}

func runKubeCredentials(stdout io.Writer, cluster string) error {
	relogin := fmt.Sprintf("run 'alpacon kube login %s'", shellQuote(cluster))

	store, err := defaultKubeSessionStore()
	if err != nil {
		return utils.NewExitError("Failed to find the home directory: %s.", err)
	}
	records, err := store.records(cluster)
	if err != nil {
		return utils.NewExitError("Failed to read the saved kube sessions: %s.", err)
	}

	// Read without the network: the config names the logged-in workspace, and
	// the login a moved expiry is saved for. Without a login the saved token is
	// still served until it expires.
	login, loginErr := config.LoadConfig()
	workspaceURL := ""
	if loginErr == nil {
		workspaceURL = login.WorkspaceURL
	}
	record := pickRecord(records, clusterServer(os.Getenv("KUBERNETES_EXEC_INFO")), workspaceURL)
	if record == nil {
		return utils.NewExitErrorWithCode(utils.ExitCodeWorkSessionDenied, "No kube session is saved for %s; %s.", cluster, relogin)
	}

	var retrieve func() (*kubernetes.KubeSessionDetail, error)
	if loginErr == nil && record.WorkspaceURL == login.WorkspaceURL {
		retrieve = func() (*kubernetes.KubeSessionDetail, error) {
			return within(retrieveTimeout, nil, func() (*kubernetes.KubeSessionDetail, error) {
				ac, err := recordClient(record)
				if err != nil {
					return nil, err
				}
				return kubernetes.GetSession(ac, record.KubeSessionID)
			})
		}
	}
	credential, checkErr := resolveCredential(record, time.Now(), retrieve)
	if errors.Is(checkErr, errSessionGone) {
		msg, msgArgs := sessionGoneMessage(cluster, checkErr)
		return utils.NewExitErrorWithCode(utils.ExitCodeWorkSessionDenied, msg, msgArgs...)
	}
	if checkErr != nil {
		utils.CliWarning("Could not check the kube session for %s (%s); using the saved token until it expires.", cluster, checkErr)
	}

	if loginErr == nil {
		saveMovedExpiry(store, login, record, credential)
	}

	if err := utils.PrintJSONValue(stdout, credential); err != nil {
		return utils.NewExitError("Failed to print the credential: %s.", err)
	}
	return nil
}

// resolveCredential builds what kubectl receives. The token is always the saved
// one: the server hands a session's token out only when it is created, so only
// the expiry can move. A failed check that leaves the token usable returns the
// credential along with its error.
func resolveCredential(record *kubeSessionRecord, now time.Time, retrieve func() (*kubernetes.KubeSessionDetail, error)) (execCredential, error) {
	expiry, err := credentialExpiry(record, now, retrieve)
	if errors.Is(err, errSessionGone) {
		return execCredential{}, err
	}
	return execCredential{
		APIVersion: execCredentialVersion,
		Kind:       "ExecCredential",
		Status:     execCredentialStatus{Token: record.ChannelToken, ExpirationTimestamp: expiry.UTC()},
	}, err
}

// pickRecord chooses the record for the server kubectl is calling, since one
// cluster name can be saved for several workspaces. Without that server it
// takes the logged-in workspace's record, and then a lone record.
func pickRecord(records []kubeSessionRecord, server, workspaceURL string) *kubeSessionRecord {
	match := func(keep func(kubeSessionRecord) bool) *kubeSessionRecord {
		for i := range records {
			if keep(records[i]) {
				return &records[i]
			}
		}
		return nil
	}
	if server != "" {
		return match(func(r kubeSessionRecord) bool { return r.ProxyURL == server })
	}
	if r := match(func(r kubeSessionRecord) bool { return r.WorkspaceURL == workspaceURL }); r != nil {
		return r
	}
	if len(records) == 1 {
		return &records[0]
	}
	return nil
}

// credentialExpiry decides how long the saved token stays good. With
// retrieve, the server's answer wins: a closed or expired session is gone,
// and an extended one moves the expiry. So is one the server will not show
// this login: a 404 is a session this account cannot see, such as after
// another account logged in without a logout, and a 401 or 403 is a refused
// check (refusedCheck). The token must not go on acting for either. Any other
// failed check, or a record of another workspace this login cannot check
// (retrieve nil), falls back to the saved expiry, so a passing outage does
// not lock kubectl out.
func credentialExpiry(record *kubeSessionRecord, now time.Time, retrieve func() (*kubernetes.KubeSessionDetail, error)) (time.Time, error) {
	saved := record.TokenExpiredAt
	if retrieve == nil {
		if !saved.After(now) {
			return time.Time{}, errSessionGone
		}
		return saved, nil
	}
	session, err := retrieve()
	if err != nil {
		switch utils.HTTPStatusCode(err) {
		case http.StatusNotFound:
			return time.Time{}, errSessionGone
		case http.StatusUnauthorized, http.StatusForbidden:
			return time.Time{}, &refusedCheck{err: err}
		}
		if !saved.After(now) {
			return time.Time{}, errSessionGone
		}
		return saved, err
	}
	if session.ClosedAt != nil {
		return time.Time{}, errSessionGone
	}
	expiry := saved
	if session.TokenExpiredAt != nil {
		expiry = *session.TokenExpiredAt
	}
	if !expiry.After(now) {
		return time.Time{}, errSessionGone
	}
	return expiry, nil
}

// within runs call and gives up after timeout, or once cancel closes, leaving
// the request to finish in the background of a process that is about to exit.
// A nil cancel never closes.
func within[T any](timeout time.Duration, cancel <-chan struct{}, call func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := call()
		done <- result{value, err}
	}()
	var zero T
	select {
	case r := <-done:
		return r.value, r.err
	case <-time.After(timeout):
		return zero, fmt.Errorf("%w within %s", errNoAnswer, timeout)
	case <-cancel:
		return zero, errCallInterrupted
	}
}

// recordClient builds a client for the record's workspace, refusing one the
// login has since moved away from: the record's session lives there.
func recordClient(record *kubeSessionRecord) (*client.AlpaconClient, error) {
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		return nil, err
	}
	if ac.BaseURL != record.WorkspaceURL {
		return nil, errors.New("the login moved to another workspace")
	}
	return ac, nil
}

// saveMovedExpiry saves the expiry the credential carries when it differs from
// the record's.
func saveMovedExpiry(store kubeSessionStore, login config.Config, record *kubeSessionRecord, credential execCredential) {
	expiry := credential.Status.ExpirationTimestamp
	if expiry.Equal(record.TokenExpiredAt) {
		return
	}
	updated := *record
	updated.TokenExpiredAt = expiry
	saveExtendedRecord(store, login, updated)
}

// saveExtendedRecord keeps a moved expiry, so the next run need not learn it
// again. A login holding the record's lock is about to replace it, and a
// logout since login removed it, so the update is skipped then; any other
// failure only costs that lookup.
func saveExtendedRecord(store kubeSessionStore, login config.Config, record kubeSessionRecord) {
	lock, err := store.lock(record.key())
	if err != nil {
		return
	}
	defer func() { _ = lock.Release() }()
	_ = config.WhileLoggedIn(login, func() error {
		current, err := store.read(record.key())
		if err != nil || current == nil || current.KubeSessionID != record.KubeSessionID {
			return nil
		}
		return store.write(record)
	})
}
