package kube

import (
	"encoding/json"
	"errors"
	"fmt"
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

var kubeCredentialsCluster string

var kubeCredentialsCmd = &cobra.Command{
	Use:   "credentials",
	Short: "Print the token kubectl uses for a cluster",
	Long: `Print a client.authentication.k8s.io/v1 ExecCredential holding the token of
the kube session 'alpacon kube login' opened for the cluster. kubectl runs this
command itself, through the context kube login writes; you do not run it.

Each run checks the session with the server. A session that has ended, or whose
work session has expired, exits 3 and says to run 'alpacon kube login' again.`,
	Hidden: true,
	Args:   cobra.NoArgs,
	Run:    runKubeCredentials,
}

func init() {
	kubeCredentialsCmd.Flags().StringVar(&kubeCredentialsCluster, "cluster", "", "cluster whose saved kube session to use")
	_ = kubeCredentialsCmd.MarkFlagRequired("cluster")
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

func runKubeCredentials(cmd *cobra.Command, args []string) {
	cluster := strings.TrimSpace(kubeCredentialsCluster)
	relogin := fmt.Sprintf("run 'alpacon kube login %s'", shellQuote(cluster))

	store, err := defaultKubeSessionStore()
	if err != nil {
		utils.CliErrorWithExit("Failed to find the home directory: %s.", err)
	}
	records, err := store.records(cluster)
	if err != nil {
		utils.CliErrorWithExit("Failed to read the saved kube sessions: %s.", err)
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
		utils.CliErrorWithExitCode(utils.ExitCodeWorkSessionDenied, "No kube session is saved for %s; %s.", cluster, relogin)
	}

	var retrieve func() (*kubernetes.KubeSessionDetail, error)
	if loginErr == nil && record.WorkspaceURL == login.WorkspaceURL {
		retrieve = func() (*kubernetes.KubeSessionDetail, error) {
			return retrieveWithin(retrieveTimeout, func() (*kubernetes.KubeSessionDetail, error) {
				ac, err := client.NewAlpaconAPIClient()
				if err != nil {
					return nil, err
				}
				if ac.BaseURL != record.WorkspaceURL {
					return nil, errors.New("the login moved to another workspace")
				}
				return kubernetes.GetSession(ac, record.KubeSessionID)
			})
		}
	}
	expiry, checkErr := credentialExpiry(record, time.Now(), retrieve)
	if errors.Is(checkErr, errSessionGone) {
		msg, msgArgs := sessionGoneMessage(cluster, checkErr)
		utils.CliErrorWithExitCode(utils.ExitCodeWorkSessionDenied, msg, msgArgs...)
	}
	if checkErr != nil {
		utils.CliWarning("Could not check the kube session for %s (%s); using the saved token until it expires.", cluster, checkErr)
	}

	if loginErr == nil && !expiry.Equal(record.TokenExpiredAt) {
		updated := *record
		updated.TokenExpiredAt = expiry
		saveExtendedRecord(store, login, updated)
	}

	if err := utils.PrintJSONValue(os.Stdout, execCredential{
		APIVersion: execCredentialVersion,
		Kind:       "ExecCredential",
		Status:     execCredentialStatus{Token: record.ChannelToken, ExpirationTimestamp: expiry.UTC()},
	}); err != nil {
		utils.CliErrorWithExit("Failed to print the credential: %s.", err)
	}
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
// check (refusedCheck), and the token must not go on acting for either. Any other failed check, or a record of another workspace this login
// cannot check (retrieve nil), falls back to the saved expiry, so a passing
// outage does not lock kubectl out.
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

// retrieveWithin runs read and gives up after timeout, leaving the request to
// finish in the background of a process that is about to exit.
func retrieveWithin(timeout time.Duration, read func() (*kubernetes.KubeSessionDetail, error)) (*kubernetes.KubeSessionDetail, error) {
	type result struct {
		session *kubernetes.KubeSessionDetail
		err     error
	}
	done := make(chan result, 1)
	go func() {
		session, err := read()
		done <- result{session, err}
	}()
	select {
	case r := <-done:
		return r.session, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("no answer within %s", timeout)
	}
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
