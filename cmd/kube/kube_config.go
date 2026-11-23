package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// contextPrefix names the cluster, user and context a kube session writes.
const contextPrefix = "alpacon-"

type kubeConfigFlagValues struct {
	workSession string
	kubeconfig  string
	proxyPort   int
}

func newKubeConfigCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	var flags kubeConfigFlagValues
	cmd := &cobra.Command{
		Use:   "config CLUSTER",
		Short: "Write a kubeconfig context for a cluster",
		Long: `Open a kube session on a cluster and write a context named alpacon-CLUSTER
into your kubeconfig, then make it the current context so kubectl, k9s, and
other clients use it right away. The session runs under --work-session, or
the WorkSession ALPACON_WORK_SESSION names, which must target the cluster with
the kube scope; create one with
'alpacon work-session create --cluster CLUSTER --scope kube'.
'alpacon kube login' finds or creates that session itself and keeps the token
out of the kubeconfig.

When the workspace requires MFA for Kubernetes exec and the WorkSession has
the kube_exec scope, the command prints an MFA link, waits up to 3 minutes for
you to finish it in the browser, and then opens the session. When your account
has no username yet, it asks for one, sets it, and then opens the session; a
non-interactive run exits 1 and names 'alpacon username set <name>'. The
kubeconfig is not locked while it waits.

The kubeconfig file is the one 'kubectl config' itself writes: --kubeconfig,
else the first existing file in KUBECONFIG (the last one listed when none
exists), else ~/.kube/config. Other clusters, users, and contexts in it are
kept, though the file is rewritten in kubectl's layout.

The context carries the session's token as a static bearer, in plain text in
the kubeconfig, until a credential plugin replaces it. The token stops working
when the WorkSession ends; run this command again for a new one.

Use --proxy-port to send kubectl through 'alpacon kube proxy' on that local
port instead of connecting to the cluster's proxy host directly.`,
		Example: `  alpacon kube config prod-east
  alpacon kube config prod-east --work-session 3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47
  alpacon kube config prod-east --kubeconfig ~/.kube/alpacon
  alpacon kube config prod-east --proxy-port 18443`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runKubeConfig(ios, cmd, args[0], flags, newClient)
		}),
	}
	cmd.Flags().StringVar(&flags.workSession, "work-session", "", "WorkSession ID to open the kube session under; it must target the cluster (default: ALPACON_WORK_SESSION)")
	cmd.Flags().StringVar(&flags.kubeconfig, "kubeconfig", "", "kubeconfig file to write (default: the file kubectl writes)")
	cmd.Flags().IntVar(&flags.proxyPort, "proxy-port", 0, "route kubectl through 'alpacon kube proxy' on this local port")
	return cmd
}

// kubeConfigResult is what 'kube config' reports. It never holds the token.
type kubeConfigResult struct {
	Context         string    `json:"context"`
	Kubeconfig      string    `json:"kubeconfig"`
	KubeSessionID   string    `json:"kube_session_id"`
	ClusterID       string    `json:"cluster_id"`
	WorkSessionID   string    `json:"work_session_id"`
	ProxyURL        string    `json:"proxy_url"`
	TokenExpiresAt  time.Time `json:"token_expires_at"`
	PreviousContext string    `json:"previous_context"`
	// RBACDriftWarning is the server's rbac_drift_warning on the opened
	// session, passed through as it came; null when the session opened
	// without one or the server is older than the field.
	RBACDriftWarning json.RawMessage `json:"rbac_drift_warning"`

	clusterName    string // the cluster as the user named it, for the drift warning
	replacedServer string // host of an entry with the same name that pointed elsewhere
	plaintext      bool   // the proxy URL is http, so the token travels unencrypted
}

// clusterLookupError and sessionCreateError mark the two requests whose
// failures the command answers in its own way. usernameErr is set when the
// username setup a refusal asked for failed; that failure is what is reported.
type clusterLookupError struct{ err error }

func (e *clusterLookupError) Error() string { return e.err.Error() }
func (e *clusterLookupError) Unwrap() error { return e.err }

type sessionCreateError struct {
	err         error
	usernameErr error
}

func (e *sessionCreateError) Error() string { return e.err.Error() }
func (e *sessionCreateError) Unwrap() error { return e.err }

func runKubeConfig(ios *utils.IOStreams, cmd *cobra.Command, cluster string, flags kubeConfigFlagValues, newClient clientFactory) error {
	clusterName := strings.TrimSpace(cluster) // as kube login and logout read it
	proxyPort := 0
	if cmd.Flags().Changed("proxy-port") {
		if err := validatePort(flags.proxyPort); err != nil {
			return utils.NewUsageExitError("--proxy-port %s.", err)
		}
		proxyPort = flags.proxyPort
	}

	ac, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}
	// The active WorkSession is not a fallback: it can only hold a server
	// session, which no kube session opens under. Without either, the server
	// refuses with the gate code and the hint names 'kube login'.
	workSessionID := flags.workSession
	if workSessionID == "" {
		workSessionID = os.Getenv(worksession.WorkSessionEnvVar)
	}

	path, err := kubeconfig.ResolvePath(flags.kubeconfig)
	if err != nil {
		return utils.NewExitError("Failed to find the kubeconfig: %s.", err)
	}
	// The file is opened, locked, and checked before the session exists: its
	// token is handed out once, so a file it cannot be written to must be
	// found out while there is still nothing to lose.
	held, err := holdKubeconfig(ios, path, func(msg string) { utils.CliErrorWithExit("%s", msg) })
	if err != nil {
		return utils.NewExitError("Failed to open the kubeconfig: %s.", err)
	}
	defer held.done()

	result, err := openKubeConfig(ac, held.hold, kubeConfigOptions{
		clusterName:   clusterName,
		workSessionID: workSessionID,
		kubeconfig:    path,
		proxyPort:     proxyPort,
		interrupted:   held.interrupt.fired.Load,
	})
	if err != nil {
		return kubeConfigError(err, clusterName, workSessionID)
	}
	// A write to a closed pipe ends the process with SIGPIPE, skipping the defer and leaving the lock file.
	held.done()

	return printKubeConfigResult(ios, ios.Out, result, ac.Insecure)
}

// interruptedMessage is how a command ends on a second Ctrl+C.
const interruptedMessage = "Interrupted; the kubeconfig may not have been written, and a kube session may have been left open until the WorkSession ends."

// heldKubeconfig is a kubeconfig opened and locked for one write, with the
// Ctrl+C watch that guards it.
type heldKubeconfig struct {
	hold      *kubeconfigHold
	interrupt *interruptWatch
}

// holdKubeconfig opens and locks the kubeconfig and watches for Ctrl+C. A second Ctrl+C exits from the
// watcher, where no deferred done runs, so it releases the lock first, which kubectl's own writes wait on.
func holdKubeconfig(ios *utils.IOStreams, path string, quit func(msg string)) (*heldKubeconfig, error) {
	hold, err := openKubeconfigHold(path)
	if err != nil {
		return nil, err
	}
	interrupt := watchInterrupt(ios, "Interrupted. Stopping when it is safe to do so. Press Ctrl+C again to quit now.", func() {
		hold.close()
		quit(interruptedMessage)
	})
	return &heldKubeconfig{hold: hold, interrupt: interrupt}, nil
}

// done stops the watch and releases the lock; calling it again does nothing.
func (h *heldKubeconfig) done() {
	h.interrupt.stop()
	h.hold.close()
}

// relockTimeout bounds how long take waits for a kubeconfig lock another
// program holds. kubectl holds it only while it writes the file. Another
// alpacon run on the same kubeconfig can hold it longer, from its session
// open through its write; take then fails and the opened session is closed,
// and running it again opens without another MFA while the step-up stays
// credited.
const relockTimeout = 5 * time.Second

const relockTick = 100 * time.Millisecond

// kubeconfigHold is kubectl's write lock on one kubeconfig. A session open
// lets go of it for the MFA wait and the username prompt, so kubectl is not
// locked out of the file while the user answers, and takes it again,
// rereading the file, once the session is open. Its methods are safe to call
// from the Ctrl+C watcher and the command at once; the command takes the
// editor it writes through once, with take.
type kubeconfigHold struct {
	path string

	mu     sync.Mutex
	editor *kubeconfig.Editor // nil while let go
	closed bool               // after close nothing takes the lock again
}

func openKubeconfigHold(path string) (*kubeconfigHold, error) {
	editor, err := kubeconfig.Open(path)
	if err != nil {
		return nil, err
	}
	return &kubeconfigHold{path: path, editor: editor}, nil
}

// letGo releases the lock until take.
func (h *kubeconfigHold) letGo() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.releaseLocked()
}

// close releases the lock for good. A second Ctrl+C calls it before the
// process exits, so a take still running on the command's goroutine must not
// create a lock file nothing would remove.
func (h *kubeconfigHold) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	h.releaseLocked()
}

func (h *kubeconfigHold) releaseLocked() {
	if h.editor != nil {
		_ = h.editor.Close()
		h.editor = nil
	}
}

var errHoldClosed = errors.New("the command is stopping")

// take returns the editor the session's context is written through, taken
// once: a close from the watcher after it leaves the editor closed, not nil,
// so the write fails as a closed editor's does instead of reaching a nil
// pointer. It returns errHoldClosed once the watcher closed the hold. When the
// lock was let go it takes it again and rereads the file, waiting up to
// timeout while another program holds the lock. kubeconfig.Open runs under the
// mutex on purpose: the second Ctrl+C's close exits the process as soon as it
// returns, so an Open it did not wait for could leave a lock file behind that
// would lock kubectl out of the file until someone removed it.
func (h *kubeconfigHold) take(timeout time.Duration) (*kubeconfig.Editor, error) {
	deadline := time.Now().Add(timeout)
	for {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, errHoldClosed
		}
		if h.editor == nil {
			editor, err := kubeconfig.Open(h.path)
			if err != nil {
				h.mu.Unlock()
				if !errors.Is(err, kubeconfig.ErrLocked) || !time.Now().Add(relockTick).Before(deadline) {
					return nil, err
				}
				time.Sleep(relockTick)
				continue
			}
			h.editor = editor
		}
		editor := h.editor
		h.mu.Unlock()
		return editor, nil
	}
}

// interruptWatch records a first Ctrl+C instead of acting on it, so the main
// goroutine stops at its next safe point: before the session is opened, or
// after, closing the session it opened. A second Ctrl+C quits at once.
type interruptWatch struct {
	fired   atomic.Bool
	first   chan struct{} // closed on the first Ctrl+C
	signals chan os.Signal
	done    chan struct{}
	exited  chan struct{} // closed when the watcher goroutine returns
	// stop ends the watch and waits for the watcher to return, so a notice it
	// is still writing is out before the command finishes and its caller, or a
	// test, takes stderr back.
	stop func()
}

func watchInterrupt(ios *utils.IOStreams, firstNotice string, quit func()) *interruptWatch {
	w := &interruptWatch{first: make(chan struct{}), signals: make(chan os.Signal, 2), done: make(chan struct{}), exited: make(chan struct{})}
	signal.Notify(w.signals, os.Interrupt, syscall.SIGTERM)
	w.stop = sync.OnceFunc(func() {
		signal.Stop(w.signals)
		close(w.done)
		<-w.exited
	})
	go func() {
		defer close(w.exited)
		for {
			select {
			case <-w.signals:
				if w.fired.Swap(true) {
					quit()
					return
				}
				// The command sees the interrupt before the notice is written,
				// so a stderr that blocks cannot hold up the cancellation; stop
				// waits for the notice instead.
				close(w.first)
				ios.Warning("%s", firstNotice)
			case <-w.done:
				return
			}
		}
	}()
	return w
}

func kubeConfigError(err error, clusterName, workSessionID string) error {
	var lookupErr *clusterLookupError
	if errors.As(err, &lookupErr) {
		// The cluster list answers 404 only when the surface is off.
		if kubernetes.IsSurfaceOff(lookupErr.err) {
			return notEnabledError()
		}
		return utils.NewExitError("Failed to find the cluster: %s.", lookupErr.err)
	}

	// An empty operation keeps the command's name in the --output json envelope.
	return kubeSessionError(err, clusterName, workSessionID, "")
}

// kubeSessionError is openKubeSession's failure, an envelope under operation: a failed username setup
// says why, a gate refusal exits 3, a refusal the server names gets its hint, and anything else says
// the kubeconfig was not written.
func kubeSessionError(err error, clusterName, workSessionID, operation string) error {
	fail := func(err error, format string, args ...any) error {
		return utils.NewEnvelopeExitError(operation, err, format, args...)
	}
	var createErr *sessionCreateError
	if errors.As(err, &createErr) {
		if createErr.usernameErr != nil {
			// The setup's own refusal names the code when it carries one; otherwise the account
			// has no username, which an older server answers kube_session_principal_not_supported.
			cause := errors.Join(createErr.usernameErr, createErr.err)
			if utils.StructuredErrorCode(createErr.usernameErr) == "" {
				cause = errors.Join(usernameUnsetError{}, createErr.usernameErr, createErr.err)
			}
			return fail(cause, "Failed to open a kube session: %s.", createErr.usernameErr)
		}
		if gateErr := utils.WorkSessionExitError(createErr.err, utils.OperationKube, clusterName, config.ResolveAuthMethod(), workSessionID); gateErr != nil {
			return gateErr
		}
		if hint := kubeSessionErrorHint(createErr.err, clusterName); hint != "" {
			return fail(createErr.err, "Failed to open a kube session: %s.", hint)
		}
		return fail(createErr.err, "Failed to open a kube session: %s.", utils.InteractiveOnly(createErr.err, "open a kube session"))
	}
	return fail(err, "The kubeconfig was not written: %s.", err)
}

// usernameUnsetError gives a failed username setup the error_code
// user_username_required, whichever code the open was refused with.
type usernameUnsetError struct{}

func (usernameUnsetError) Error() string       { return "the account has no username" }
func (usernameUnsetError) ErrorCode() string   { return utils.UsernameRequired }
func (usernameUnsetError) ErrorSource() string { return "" }

// kubeSessionErrorHint says what to do about a refusal the server names with
// a kube-session code, or returns "" for any other error.
func kubeSessionErrorHint(err error, clusterName string) string {
	// The server's hint is the guidance when the refusal carries one, and the
	// caller's fallback line prints it under the error.
	if utils.RefusalHint(err) != "" {
		return ""
	}
	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case utils.ClusterNotConnected:
		return fmt.Sprintf("the agent in cluster %s is not connected; check 'alpacon kube describe %s' and try again", clusterName, clusterName)
	case utils.KubeSessionChannelOpenFailed:
		return "the cluster's agent did not accept the session, so it was closed; try again"
	case utils.KubeSessionPrincipalNotSupported:
		return "this account's username cannot be used as a kube session's identity: it is unset, or it is one the server refuses, such as a name starting with 'system:'"
	case utils.ClusterRBACDrift:
		return fmt.Sprintf("cluster %s has RBAC bindings outside Alpacon that grant sessions more than Alpacon does, and the workspace policy refuses new sessions on it until a cluster admin removes them. The cluster's agent logs an external_grant line for each such binding so the cluster admin can find them there", clusterName)
	}
	return ""
}

type kubeConfigOptions struct {
	clusterName   string
	workSessionID string
	kubeconfig    string
	proxyPort     int         // 0 leaves kubectl connecting directly
	interrupted   func() bool // nil when nothing can interrupt the command
}

var errInterrupted = errors.New("interrupted before the kubeconfig was written")

func (o kubeConfigOptions) isInterrupted() bool {
	return o.interrupted != nil && o.interrupted()
}

// openKubeConfig opens a kube session on the cluster and writes it into the
// kubeconfig hold locks. A session whose token cannot be written is closed
// again, since nothing could ever use it.
func openKubeConfig(ac *client.AlpaconClient, hold *kubeconfigHold, opts kubeConfigOptions) (*kubeConfigResult, error) {
	clusterID, err := kubernetes.GetClusterIDByName(ac, opts.clusterName)
	if err != nil {
		return nil, &clusterLookupError{err: err}
	}
	return openKubeSession(ac, hold, opts, clusterID, kubeSessionWriter{})
}

// kubeSessionWriter is what differs between the commands that write a kube
// session into a kubeconfig: how kubectl gets its token, and what else is
// kept of the session.
type kubeSessionWriter struct {
	// exec, when set, has kubectl run a credential plugin for the token
	// instead of reading the session's token from the kubeconfig.
	exec *kubeconfig.ExecUser
	// persist keeps the opened session before the kubeconfig is written; an
	// error closes the session. The undo it returns reverses it when the
	// kubeconfig write then fails.
	persist func(session *kubernetes.KubeSessionResponse) (undo func() error, err error)
}

// openKubeSession opens a kube session under the work session and writes its
// context. Any failure after the session exists closes it again.
func openKubeSession(ac *client.AlpaconClient, hold *kubeconfigHold, opts kubeConfigOptions, clusterID string, w kubeSessionWriter) (*kubeConfigResult, error) {
	if opts.isInterrupted() {
		return nil, errInterrupted
	}
	session, err := createKubeSession(ac, hold, opts, kubernetes.KubeSessionCreateRequest{
		Cluster:     clusterID,
		WorkSession: opts.workSessionID,
		ClientType:  kubernetes.ClientTypeCLI,
	})
	if err != nil {
		return nil, err
	}

	discard := func(reason error) error {
		if closeErr := kubernetes.CloseSession(ac, session.ID); closeErr != nil {
			return fmt.Errorf("%w; closing the kube session %s also failed: %w", reason, session.ID, closeErr)
		}
		return fmt.Errorf("%w; the kube session was closed, so run the command again", reason)
	}

	// Only a lock let go for MFA or username setup is opened again here, so any
	// error but errHoldClosed comes from taking it back.
	editor, err := hold.take(relockTimeout)
	if err != nil {
		if errors.Is(err, errHoldClosed) {
			return nil, discard(errInterrupted)
		}
		return nil, discard(fmt.Errorf("the kubeconfig could not be opened again: %w", err))
	}

	if opts.isInterrupted() {
		return nil, discard(errInterrupted)
	}

	plaintext, err := checkProxyURL(session.ProxyURL, opts.proxyPort)
	if err != nil {
		return nil, discard(err)
	}
	if session.ChannelToken == "" {
		return nil, discard(errors.New("the server returned no token for the kube session"))
	}

	undo := func() error { return nil }
	if w.persist != nil {
		if undo, err = w.persist(session); err != nil {
			return nil, discard(err)
		}
	}

	entry := kubeconfig.Entry{
		Name:                  contextPrefix + opts.clusterName,
		Server:                session.ProxyURL,
		Exec:                  w.exec,
		InsecureSkipTLSVerify: ac.Insecure,
	}
	if w.exec == nil {
		entry.Token = session.ChannelToken
	}
	if opts.proxyPort != 0 {
		entry.ProxyURL = localProxyURL(opts.proxyPort)
	}
	previous, replaced := editor.Apply(entry)
	if err := editor.Save(); err != nil {
		err = fmt.Errorf("failed to write %s: %w", opts.kubeconfig, err)
		if undoErr := undo(); undoErr != nil {
			err = fmt.Errorf("%w; restoring the saved kube session also failed: %w", err, undoErr)
		}
		return nil, discard(err)
	}

	return &kubeConfigResult{
		Context:          entry.Name,
		Kubeconfig:       opts.kubeconfig,
		KubeSessionID:    session.ID,
		ClusterID:        clusterID,
		WorkSessionID:    opts.workSessionID,
		ProxyURL:         session.ProxyURL,
		TokenExpiresAt:   session.TokenExpiredAt,
		PreviousContext:  previous,
		RBACDriftWarning: session.RBACDriftWarning,
		clusterName:      opts.clusterName,
		replacedServer:   replaced,
		plaintext:        plaintext,
	}, nil
}

// handleKubeUsernameRequired is a test seam; a test that replaces it stays serial.
var handleKubeUsernameRequired = iam.HandleUsernameRequiredUnless

// createKubeSession opens the session, running the MFA step-up and the wait
// for it, and the username prompt once, in whichever order the server asks.
// The MFA link names no server, since a kube session has a cluster. The
// kubeconfig lock is let go for both, and the caller's take opens it again.
// A refusal is returned as a sessionCreateError and a first Ctrl+C as
// errInterrupted.
func createKubeSession(ac *client.AlpaconClient, hold *kubeconfigHold, opts kubeConfigOptions, req kubernetes.KubeSessionCreateRequest) (*kubernetes.KubeSessionResponse, error) {
	var session *kubernetes.KubeSessionResponse
	// refusalCode is read off the create's own tagged error, not off what
	// HandleCommonErrors returns, which can carry the MFA link lookup's code.
	var refusalCode string
	create := func() error {
		refusalCode = ""
		// Untagged, so the wait ends here instead of opening a session the
		// interrupt would only close again.
		if opts.isInterrupted() {
			return errInterrupted
		}
		var err error
		session, err = kubernetes.CreateSession(ac, req)
		if utils.IsSubmissionError(err) {
			refusalCode = utils.StructuredErrorCode(err)
		}
		return err
	}
	callbacks := mfa.WorkspaceErrorCallbacks(ac, create)
	stepUp := callbacks.OnMFARequired
	callbacks.OnMFARequired = func(serverName string) error {
		// A first Ctrl+C stops here, before a link is fetched and a browser
		// opened for a wait that would end anyway.
		if opts.isInterrupted() {
			return errInterrupted
		}
		hold.letGo()
		return stepUp(serverName)
	}
	open := func() error {
		return utils.HandleCommonErrors(create(), "", callbacks)
	}

	err := open()
	// The MFA wait ends as soon as a create returns a username refusal.
	if err != nil && needsUsername(ac, refusalCode) {
		if opts.isInterrupted() {
			return nil, errInterrupted
		}
		hold.letGo()
		_, usernameErr := handleKubeUsernameRequired(ac, opts.isInterrupted)
		if opts.isInterrupted() {
			return nil, errInterrupted
		}
		if usernameErr != nil {
			return nil, &sessionCreateError{err: err, usernameErr: usernameErr}
		}
		err = open()
	}
	if err != nil {
		if errors.Is(err, errInterrupted) {
			return nil, errInterrupted
		}
		return nil, &sessionCreateError{err: err}
	}
	return session, nil
}

// needsUsername reports whether a session open refused with code asks for a
// username the account can still set. An older server answers a null
// username with kube_session_principal_not_supported, the code it also gives
// a name it refuses, so the account is read once to tell the two apart; a
// failed read keeps the refusal.
func needsUsername(ac *client.AlpaconClient, code string) bool {
	switch code {
	case utils.UsernameRequired:
		return true
	case utils.KubeSessionPrincipalNotSupported:
		unset, err := iam.CurrentUsernameUnset(ac)
		return err == nil && unset
	}
	return false
}

// checkProxyURL reports whether proxyURL is plain http, and refuses one that
// is not an http(s) URL with a host. 'kube proxy' relays CONNECT, which only
// an https server is reached through, so --proxy-port needs https.
func checkProxyURL(proxyURL string, proxyPort int) (plaintext bool, err error) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false, fmt.Errorf("the server returned an unusable proxy URL %q", proxyURL)
	}
	if u.Scheme == "http" && proxyPort != 0 {
		return true, errors.New("this workspace serves kube sessions over plain http, which 'alpacon kube proxy' cannot relay; run without --proxy-port")
	}
	return u.Scheme == "http", nil
}

func localProxyURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("must be a port number from 1 to 65535, not %d", port)
	}
	return nil
}

func printKubeConfigResult(ios *utils.IOStreams, stdout io.Writer, result *kubeConfigResult, insecure bool) error {
	printKubeConfigWarnings(ios, result, insecure)

	if ios.IsJSON() {
		if err := utils.PrintJSONValue(stdout, result); err != nil {
			return utils.NewExitError("Failed to print the result: %s.", err)
		}
		return nil
	}

	ios.Info("Context %q written to %s (token expires %s).", result.Context, result.Kubeconfig, formatTime(&result.TokenExpiresAt, time.Local))
	printKubeConfigNext(ios, result)
	return nil
}

// printKubeConfigWarnings says what about the written context the user may
// not expect, in either output format.
func printKubeConfigWarnings(ios *utils.IOStreams, result *kubeConfigResult, insecure bool) {
	if result.plaintext {
		ios.Warning("The proxy URL %s is plain http, so kubectl will send the session token unencrypted.", result.ProxyURL)
	}
	if insecure {
		ios.Warning("This workspace skips TLS certificate verification, so the context written for kubectl skips it too.")
	}
	if result.replacedServer != "" {
		ios.Warning("The %s entries pointed at %s and now point at this workspace.", result.Context, result.replacedServer)
	}
	if kubeconfig.Shadowed(result.Kubeconfig) {
		ios.Warning("KUBECONFIG lists files before %s; kubectl takes current-context and %s from the first file that sets them.", result.Kubeconfig, result.Context)
	}
	if line := rbacDriftWarningLine(result.clusterName, result.RBACDriftWarning); line != "" {
		ios.Warning("%s", line)
	}
}

// rbacDriftWarningLine says that the session opened on a cluster whose RBAC
// bindings made outside Alpacon widen what its identity may do, or returns ""
// when the session carries no such warning. The session is open, so this is
// information, not a failure. The counts are named when they can be read, as
// a lower bound when the cluster's check did not finish.
func rbacDriftWarningLine(clusterName string, raw json.RawMessage) string {
	warning, present := kubernetes.ParseRBACDriftWarning(raw)
	if !present {
		return ""
	}
	detail := ""
	if counts := kubernetes.ExternalGrantCounts(warning); counts != "" {
		if warning.Incomplete {
			detail = fmt.Sprintf(" (external grants: at least %s; the cluster's check did not finish)", counts)
		} else {
			detail = fmt.Sprintf(" (external grants: %s)", counts)
		}
	}
	return fmt.Sprintf("Cluster %q has RBAC bindings made outside Alpacon that widen what this session's identity may do%s; the workspace policy allows the session anyway.", clusterName, detail)
}

func printKubeConfigNext(ios *utils.IOStreams, result *kubeConfigResult) {
	if result.PreviousContext != "" && result.PreviousContext != result.Context {
		ios.Info("Switched current-context from %q.", result.PreviousContext)
	}
	if kubeconfig.ReadByDefault(result.Kubeconfig) {
		ios.Info("Next: kubectl get pods -A")
	} else {
		ios.Info("Next: kubectl --kubeconfig %s get pods -A", shellQuote(result.Kubeconfig))
	}
}

// shellQuote leaves a plain path as it is and quotes any other, so the printed
// command can be pasted into the platform's usual shell.
func shellQuote(s string) string {
	return shellQuoteFor(runtime.GOOS, s)
}

// shellQuoteFor quotes for a POSIX shell, or on Windows with double quotes,
// which both cmd.exe and PowerShell read; a Windows path cannot hold one.
func shellQuoteFor(goos, s string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:+=,@"
	if goos == "windows" {
		if s != "" && strings.Trim(s, safe+`\`) == "" {
			return s
		}
		return `"` + s + `"`
	}
	if s != "" && strings.Trim(s, safe) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
