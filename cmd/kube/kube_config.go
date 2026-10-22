package kube

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
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

var kubeConfigFlags kubeConfigFlagValues

var kubeConfigCmd = &cobra.Command{
	Use:   "config CLUSTER",
	Short: "Write a kubeconfig context for a cluster",
	Long: `Open a kube session on a cluster and write a context named alpacon-CLUSTER
into your kubeconfig, then make it the current context so kubectl, k9s, and
other clients use it right away. The session runs under --work-session, or the
active WorkSession, which must target the cluster with the kube scope; create
one with 'alpacon work-session create --cluster CLUSTER --scope kube'.
'alpacon kube login' finds or creates that session itself and keeps the token
out of the kubeconfig.

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
	Run:  runKubeConfig,
}

func init() {
	kubeConfigCmd.Flags().StringVar(&kubeConfigFlags.workSession, "work-session", "", "WorkSession ID to open the kube session under; it must target the cluster (default: the active one)")
	kubeConfigCmd.Flags().StringVar(&kubeConfigFlags.kubeconfig, "kubeconfig", "", "kubeconfig file to write (default: the file kubectl writes)")
	kubeConfigCmd.Flags().IntVar(&kubeConfigFlags.proxyPort, "proxy-port", 0, "route kubectl through 'alpacon kube proxy' on this local port")
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

	replacedServer string // host of an entry with the same name that pointed elsewhere
	plaintext      bool   // the proxy URL is http, so the token travels unencrypted
}

// clusterLookupError and sessionCreateError mark the two requests whose
// failures the command answers in its own way.
type clusterLookupError struct{ err error }

func (e *clusterLookupError) Error() string { return e.err.Error() }
func (e *clusterLookupError) Unwrap() error { return e.err }

type sessionCreateError struct{ err error }

func (e *sessionCreateError) Error() string { return e.err.Error() }
func (e *sessionCreateError) Unwrap() error { return e.err }

func runKubeConfig(cmd *cobra.Command, args []string) {
	clusterName := args[0]
	proxyPort := 0
	if cmd.Flags().Changed("proxy-port") {
		if err := validatePort(kubeConfigFlags.proxyPort); err != nil {
			utils.CliErrorWithExit("--proxy-port %s.", err)
		}
		proxyPort = kubeConfigFlags.proxyPort
	}

	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}
	workSessionID, err := worksession.ResolveFor(kubeConfigFlags.workSession, ac.WorkspaceName)
	if err != nil {
		utils.CliErrorWithExit("%s", err)
	}

	path, err := kubeconfig.ResolvePath(kubeConfigFlags.kubeconfig)
	if err != nil {
		utils.CliErrorWithExit("Failed to find the kubeconfig: %s.", err)
	}
	// The file is opened, locked, and checked before the session exists: its
	// token is handed out once, so a file it cannot be written to must be
	// found out while there is still nothing to lose.
	editor, err := kubeconfig.Open(path)
	if err != nil {
		utils.CliErrorWithExit("Failed to open the kubeconfig: %s.", err)
	}
	// Every exit below runs through os.Exit, which skips deferred calls, so
	// each releases the kubeconfig lock first; a lock left behind would block
	// kubectl's own writes to the file. OnceFunc makes a release from the
	// interrupt watcher and one from here safe to overlap.
	release := sync.OnceFunc(func() { _ = editor.Close() })
	defer release()
	interrupt := watchInterrupt(func() {
		release()
		utils.CliErrorWithExit("Interrupted; the kubeconfig may not have been written, and a kube session may have been left open until the WorkSession ends.")
	})
	defer interrupt.stop()

	result, err := openKubeConfig(ac, editor, kubeConfigOptions{
		clusterName:   clusterName,
		workSessionID: workSessionID,
		kubeconfig:    path,
		proxyPort:     proxyPort,
		interrupted:   interrupt.fired.Load,
	})
	// The file is written or abandoned either way, so nothing below needs the
	// lock, and a failed write to stdout cannot strand it.
	interrupt.stop()
	release()
	if err != nil {
		exitKubeConfigError(err, clusterName, workSessionID)
	}

	printKubeConfigResult(result, ac.Insecure)
}

// interruptWatch records a first Ctrl+C instead of acting on it, so the main
// goroutine stops at its next safe point: before the session is opened, or
// after, closing the session it opened. A second Ctrl+C quits at once.
type interruptWatch struct {
	fired   atomic.Bool
	signals chan os.Signal
	done    chan struct{}
	stop    func()
}

func watchInterrupt(quit func()) *interruptWatch {
	w := &interruptWatch{signals: make(chan os.Signal, 2), done: make(chan struct{})}
	signal.Notify(w.signals, os.Interrupt, syscall.SIGTERM)
	w.stop = sync.OnceFunc(func() {
		signal.Stop(w.signals)
		close(w.done)
	})
	go func() {
		for {
			select {
			case <-w.signals:
				if w.fired.Swap(true) {
					quit()
					return
				}
				utils.CliWarning("Interrupted; stopping once the kube session is closed. Press Ctrl+C again to quit now.")
			case <-w.done:
				return
			}
		}
	}()
	return w
}

func exitKubeConfigError(err error, clusterName, workSessionID string) {
	var lookupErr *clusterLookupError
	if errors.As(err, &lookupErr) {
		// The cluster list answers 404 only when the surface is off.
		if kubernetes.IsSurfaceOff(lookupErr.err) {
			exitNotEnabled()
		}
		utils.CliErrorWithExit("Failed to find the cluster: %s.", lookupErr.err)
	}

	exitKubeSessionError(err, clusterName, workSessionID, func(_ error, format string, args ...any) {
		utils.CliErrorWithExit(format, args...)
	})
}

// exitKubeSessionError reports a failure of openKubeSession through fail,
// which ends the command: a WorkSession gate refusal exits 3 on its own, a
// refusal the server names gets its hint, and anything else says the
// kubeconfig was not written.
func exitKubeSessionError(err error, clusterName, workSessionID string, fail func(err error, format string, args ...any)) {
	var createErr *sessionCreateError
	if errors.As(err, &createErr) {
		utils.HandleWorkSessionError(createErr.err, utils.OperationKube, clusterName, config.ResolveAuthMethod(), workSessionID)
		if hint := kubeSessionErrorHint(createErr.err, clusterName); hint != "" {
			fail(createErr.err, "Failed to open a kube session: %s.", hint)
		}
		fail(createErr.err, "Failed to open a kube session: %s.", utils.InteractiveOnly(createErr.err, "open a kube session"))
	}
	fail(err, "The kubeconfig was not written: %s.", err)
}

// kubeSessionErrorHint says what to do about a refusal the server names with
// a kube-session code, or returns "" for any other error.
func kubeSessionErrorHint(err error, clusterName string) string {
	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case utils.ClusterNotConnected:
		return fmt.Sprintf("the agent in cluster %s is not connected; check 'alpacon kube describe %s' and try again", clusterName, clusterName)
	case utils.KubeSessionChannelOpenFailed:
		return "the cluster's agent did not accept the session, so it was closed; try again"
	case utils.KubeSessionPrincipalNotSupported:
		return "this account has no user name a kube session can act as; log in as a user"
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
// kubeconfig editor holds. A session whose token cannot be written is closed
// again, since nothing could ever use it.
func openKubeConfig(ac *client.AlpaconClient, editor *kubeconfig.Editor, opts kubeConfigOptions) (*kubeConfigResult, error) {
	clusterID, err := kubernetes.GetClusterIDByName(ac, opts.clusterName)
	if err != nil {
		return nil, &clusterLookupError{err: err}
	}
	if opts.isInterrupted() {
		return nil, errInterrupted
	}
	return openKubeSession(ac, editor, opts, clusterID, kubeSessionWriter{})
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
func openKubeSession(ac *client.AlpaconClient, editor *kubeconfig.Editor, opts kubeConfigOptions, clusterID string, w kubeSessionWriter) (*kubeConfigResult, error) {
	session, err := kubernetes.CreateSession(ac, kubernetes.KubeSessionCreateRequest{
		Cluster:     clusterID,
		WorkSession: opts.workSessionID,
		ClientType:  kubernetes.ClientTypeCLI,
	})
	if err != nil {
		return nil, &sessionCreateError{err: err}
	}

	discard := func(reason error) error {
		if closeErr := kubernetes.CloseSession(ac, session.ID); closeErr != nil {
			return fmt.Errorf("%w; closing the kube session %s also failed: %w", reason, session.ID, closeErr)
		}
		return fmt.Errorf("%w; the kube session was closed, so run the command again", reason)
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
		Context:         entry.Name,
		Kubeconfig:      opts.kubeconfig,
		KubeSessionID:   session.ID,
		ClusterID:       clusterID,
		WorkSessionID:   opts.workSessionID,
		ProxyURL:        session.ProxyURL,
		TokenExpiresAt:  session.TokenExpiredAt,
		PreviousContext: previous,
		replacedServer:  replaced,
		plaintext:       plaintext,
	}, nil
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

func printKubeConfigResult(result *kubeConfigResult, insecure bool) {
	printKubeConfigWarnings(result, insecure)

	if utils.OutputFormat == utils.OutputFormatJSON {
		if err := utils.PrintJSONValue(os.Stdout, result); err != nil {
			utils.CliErrorWithExit("Failed to print the result: %s.", err)
		}
		return
	}

	utils.CliInfo("Context %q written to %s (token expires %s).", result.Context, result.Kubeconfig, formatTime(&result.TokenExpiresAt))
	printKubeConfigNext(result)
}

// printKubeConfigWarnings says what about the written context the user may
// not expect, in either output format.
func printKubeConfigWarnings(result *kubeConfigResult, insecure bool) {
	if result.plaintext {
		utils.CliWarning("The proxy URL %s is plain http, so kubectl will send the session token unencrypted.", result.ProxyURL)
	}
	if insecure {
		utils.CliWarning("This workspace skips TLS certificate verification, so the context written for kubectl skips it too.")
	}
	if result.replacedServer != "" {
		utils.CliWarning("The %s entries pointed at %s and now point at this workspace.", result.Context, result.replacedServer)
	}
	if kubeconfig.Shadowed(result.Kubeconfig) {
		utils.CliWarning("KUBECONFIG lists files before %s; kubectl takes current-context and %s from the first file that sets them.", result.Kubeconfig, result.Context)
	}
}

func printKubeConfigNext(result *kubeConfigResult) {
	if result.PreviousContext != "" && result.PreviousContext != result.Context {
		utils.CliInfo("Switched current-context from %q.", result.PreviousContext)
	}
	if kubeconfig.ReadByDefault(result.Kubeconfig) {
		utils.CliInfo("Next: kubectl get pods -A")
	} else {
		utils.CliInfo("Next: kubectl --kubeconfig %s get pods -A", shellQuote(result.Kubeconfig))
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
