package kube

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	opKubeLogout = "kube_logout"

	// logoutInterruptMessage is what a first Ctrl+C prints. The command stops at
	// its next safe point, because a context removed without its token would
	// leave the token on disk with nothing pointing a later logout at it.
	logoutInterruptMessage = "Interrupted; finishing what was started on this machine. Press Ctrl+C again to quit now."
)

func newKubeLogoutCmd() *cobra.Command {
	var kubeconfigFlag string
	cmd := &cobra.Command{
		Use:   "logout [CLUSTER]",
		Short: "Log out of a cluster and close its kube session",
		Long: `Remove the alpacon-CLUSTER context, cluster and user from your kubeconfig,
delete the token saved for the cluster, then ask the server to close the kube
session. Without CLUSTER, the current context is logged out if alpacon wrote it.

Everything on this machine is removed first. When the server cannot close the
session, because it cannot be reached or the session belongs to a workspace you
are not logged in to, the command warns and still succeeds; the session stays
open until its WorkSession ends. The WorkSession itself is not ended, and other
contexts are left as they were.

Without --kubeconfig, every file kubectl reads is cleaned: each one in
KUBECONFIG, else ~/.kube/config. A current-context that named the removed context
is unset, and kubectl then uses the next file's current-context, if any. Pick
another with 'kubectl config use-context NAME'.`,
		Example: `  alpacon kube logout prod-east
  alpacon kube logout
  alpacon kube logout prod-east --kubeconfig ~/.kube/alpacon`,
		Args: cobra.MaximumNArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runKubeLogoutCommand(cmd.OutOrStdout(), args, kubeconfigFlag)
		}),
	}
	cmd.Flags().StringVar(&kubeconfigFlag, "kubeconfig", "", "kubeconfig file to clean (default: every file kubectl reads)")
	return cmd
}

var (
	errLogoutLocksReleased = errors.New("the logout is quitting")
	// errLogoutInterrupted is a first Ctrl+C that arrived before any edit.
	errLogoutInterrupted = errors.New("interrupted, so nothing was changed")
)

// logoutNotice is a warning the command prints. It keeps the arguments apart so
// an error among them still shows the hint a server refusal carries.
type logoutNotice struct {
	format string
	args   []any
}

// loggedOutSession is what became of one saved kube session.
type loggedOutSession struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Closed    bool   `json:"closed"`
	// NotFound is a 404 on the close: the session has ended, or this login
	// cannot see it because another account opened it.
	NotFound   bool   `json:"not_found"`
	CloseError string `json:"close_error,omitempty"`
}

// kubeLogoutResult is what 'kube logout' reports. It never holds a token.
type kubeLogoutResult struct {
	Cluster               string             `json:"cluster"`
	Context               string             `json:"context"`
	Found                 bool               `json:"found"`
	Kubeconfigs           []string           `json:"kubeconfigs"`
	CurrentContextCleared bool               `json:"current_context_cleared"`
	EffectiveContext      string             `json:"effective_context"`
	Sessions              []loggedOutSession `json:"sessions"`

	warnings     []logoutNotice
	currentUnset bool // the merged current-context was read after the edit and is empty
	// keptElsewhere is set when --kubeconfig named a file without the context
	// and the saved tokens were kept on purpose, so "nothing found" would be wrong.
	keptElsewhere bool
}

func (r *kubeLogoutResult) warn(format string, args ...any) {
	r.warnings = append(r.warnings, logoutNotice{format: format, args: args})
}

// kubeLogoutInput is everything the logout reads from outside itself.
type kubeLogoutInput struct {
	cluster      string
	paths        []string // the kubeconfig files to clean
	explicit     bool     // paths is the one file --kubeconfig named
	store        kubeSessionStore
	workspaceURL string // the logged-in workspace; empty when not logged in
	// closer closes a saved session on the server, giving up once cancel
	// closes. It is called only for a record of the logged-in workspace, with
	// every lock already released.
	closer func(record kubeSessionRecord, cancel <-chan struct{}) error
	// save writes an edited file; nil is Editor.Save.
	save func(*kubeconfig.Editor) error
	// interrupted reports a first Ctrl+C; nil watches for one.
	interrupted func() bool
}

// logoutFile is a kubeconfig file that holds something of the context.
type logoutFile struct {
	path    string
	holding kubeconfig.Holding
}

// logoutLocks holds what the local cleanup edits under: the same order as kube
// login, store locks first, so a logout cannot take the kubeconfig out from under
// a login whose approval just arrived. mu is held across each acquire, because a
// second Ctrl+C releases from the watcher's goroutine and then exits, and a lock
// file created but not yet listed here would outlive the process and block kubectl.
type logoutLocks struct {
	mu       sync.Mutex
	released bool
	stores   map[recordKey]*filelock.Lock
	editors  []*kubeconfig.Editor
}

func (l *logoutLocks) lockStore(store kubeSessionStore, key recordKey) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return errLogoutLocksReleased
	}
	if _, held := l.stores[key]; held {
		return nil
	}
	lock, err := store.lock(key)
	if errors.Is(err, errStoreLocked) {
		return fmt.Errorf("%w; let it finish or stop it first", err)
	}
	if err != nil {
		return fmt.Errorf("failed to lock the saved kube session: %w", err)
	}
	if l.stores == nil {
		l.stores = map[recordKey]*filelock.Lock{}
	}
	l.stores[key] = lock
	return nil
}

func (l *logoutLocks) heldKeys() []recordKey {
	l.mu.Lock()
	defer l.mu.Unlock()
	keys := make([]recordKey, 0, len(l.stores))
	for key := range l.stores {
		keys = append(keys, key)
	}
	return keys
}

// open opens and locks a kubeconfig file.
func (l *logoutLocks) open(path string) (*kubeconfig.Editor, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil, errLogoutLocksReleased
	}
	ed, err := kubeconfig.Open(path)
	if err != nil {
		return nil, err
	}
	l.editors = append(l.editors, ed)
	return ed, nil
}

func (l *logoutLocks) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	for _, ed := range l.editors {
		_ = ed.Close()
	}
	for _, lock := range l.stores {
		_ = lock.Release()
	}
}

func runKubeLogoutCommand(stdout io.Writer, args []string, kubeconfigFlag string) error {
	paths, err := kubeconfig.SearchPaths(kubeconfigFlag)
	if err != nil {
		return utils.NewEnvelopeExitError(opKubeLogout, err, "Failed to find the kubeconfig: %s.", err)
	}
	paths = kubeconfig.UniqueFiles(paths)

	var cluster string
	if len(args) == 1 {
		if cluster = strings.TrimSpace(args[0]); cluster == "" {
			return utils.NewUsageEnvelopeExitError(opKubeLogout, "CLUSTER must name a cluster.")
		}
	} else if cluster, err = clusterOfCurrentContext(paths); err != nil {
		return utils.NewEnvelopeExitError(opKubeLogout, err, "Cannot pick the cluster to log out of: %s.", err)
	}

	store, err := defaultKubeSessionStore()
	if err != nil {
		return utils.NewEnvelopeExitError(opKubeLogout, err, "Failed to find the home directory: %s.", err)
	}
	// Read without the network. Not being logged in leaves only the server's
	// half undone: the context and the saved token still go.
	workspaceURL := ""
	if login, err := config.LoadConfig(); err == nil {
		workspaceURL = login.WorkspaceURL
	}

	result, logoutErr := kubeLogout(kubeLogoutInput{
		cluster:      cluster,
		paths:        paths,
		explicit:     kubeconfigFlag != "",
		store:        store,
		workspaceURL: workspaceURL,
		closer:       closeSavedSession,
	})
	for _, w := range result.warnings {
		utils.CliWarning(w.format, w.args...)
	}
	if logoutErr == nil {
		return printKubeLogoutResult(stdout, result, true)
	}
	// What was done before the failure is still reported: the files edited and
	// the sessions closed are gone either way.
	if result.Found {
		if err := printKubeLogoutResult(stdout, result, false); err != nil {
			return err
		}
	}
	if expectedLogoutFailure(logoutErr) {
		return utils.NewEnvelopeExitErrorWithCode(utils.ExitCodeGeneralError, opKubeLogout, logoutErr, "Logout of %s did not finish: %s.", cluster, logoutErr)
	}
	return utils.NewEnvelopeExitError(opKubeLogout, logoutErr, "Logout of %s did not finish: %s.", cluster, logoutErr)
}

// expectedLogoutFailure is a refusal the user can act on, which leaves out the
// "report an issue" footer.
func expectedLogoutFailure(err error) bool {
	return errors.Is(err, errStoreLocked) || errors.Is(err, errLogoutInterrupted) || errors.Is(err, kubeconfig.ErrLocked)
}

// closeSavedSession closes the record's session under closeTimeout, which also
// covers the access-token refresh building the client can run.
func closeSavedSession(record kubeSessionRecord, cancel <-chan struct{}) error {
	_, err := within(closeTimeout, cancel, func() (struct{}, error) {
		ac, err := recordClient(&record)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, kubernetes.CloseSession(ac, record.KubeSessionID)
	})
	return err
}

// clusterOfCurrentContext names the cluster behind kubectl's current context,
// refusing one alpacon did not write: the name alone could be anyone's, and the
// logout deletes its cluster and user too. Naming CLUSTER fixes each refusal,
// so each is a usage error.
func clusterOfCurrentContext(paths []string) (string, error) {
	current, err := kubeconfig.CurrentContext(paths)
	if err != nil {
		return "", fmt.Errorf("failed to read the current context: %w", err)
	}
	if current == "" {
		return "", utils.MarkUsageError(errors.New("no current context is set; name the cluster: alpacon kube logout CLUSTER"))
	}
	notOurs := utils.MarkUsageError(fmt.Errorf("the current context %q was not written by alpacon; name the cluster: alpacon kube logout CLUSTER", current))
	cluster, ok := strings.CutPrefix(current, contextPrefix)
	if !ok || cluster == "" {
		return "", notOurs
	}
	// kubectl reads the context from the first file that defines it, and its
	// user from the first file that defines that user.
	targets := kubeconfig.ContextTargets(paths, current)
	if len(targets) == 0 {
		return "", notOurs
	}
	if t := targets[0]; t.PluginCluster != cluster && (t.PluginCluster != "" || !t.StaticToken) {
		return "", notOurs
	}
	return cluster, nil
}

// quitLogout is what a second Ctrl+C runs: every lock goes first, since the
// exit from the watcher's goroutine skips the deferred releases.
func quitLogout(locks *logoutLocks, localDone *atomic.Bool, exit func(msg string)) func() {
	return func() {
		locks.release()
		if localDone.Load() {
			exit("Interrupted while closing the kube session. Everything on this machine was removed; the session stays open until its WorkSession ends.")
			return
		}
		exit("Interrupted; the logout may be only partly done, so run it again.")
	}
}

func exitLogoutInterrupted(msg string) {
	utils.CliErrorEnvelopeWithExitCode(utils.ExitCodeGeneralError, opKubeLogout, nil, "%s", msg)
}

// kubeLogout removes the context and the saved token, then asks the server to
// close the session. A failure before the first edit changes nothing. A failure
// after it does not stop the rest, since the token must not outlive the context,
// and is returned once everything else is done.
func kubeLogout(in kubeLogoutInput) (*kubeLogoutResult, error) {
	context := contextPrefix + in.cluster
	result := &kubeLogoutResult{Cluster: in.cluster, Context: context, Kubeconfigs: []string{}, Sessions: []loggedOutSession{}}

	locks := &logoutLocks{}
	var localDone atomic.Bool
	// The watch runs until the command returns, so a first Ctrl+C during the
	// close gives up on the close rather than killing the process before its
	// result is printed.
	watch := watchInterrupt(logoutInterruptMessage, quitLogout(locks, &localDone, exitLogoutInterrupted))
	defer watch.stop()
	defer locks.release()
	interrupted := watch.fired.Load
	if in.interrupted != nil {
		interrupted = in.interrupted
	}

	if err := lockLogoutRecords(in, locks, context); err != nil {
		return result, err
	}
	files, saved, picked, err := findLogoutTargets(result, in, context)
	if err != nil {
		return result, err
	}
	records, err := lockRecords(in.store, locks, picked)
	if err != nil {
		return result, err
	}
	if in.explicit && len(files) == 0 {
		// pickLogoutRecords kept every record on purpose, so none of them
		// waits on a login to another workspace.
		if len(saved) > 0 {
			result.keptElsewhere = true
			result.warn("%s is not in %s, so the token saved for %s was kept.", context, strings.Join(in.paths, ", "), in.cluster)
		}
	} else {
		warnOtherRecords(result, saved, picked)
	}
	unreadable := unreadableLoginRecord(in, records)

	editors := make([]*kubeconfig.Editor, 0, len(files))
	for _, f := range files {
		ed, err := locks.open(f.path)
		if err != nil {
			return result, err
		}
		editors = append(editors, ed)
	}
	// What kubectl used before the edit, to tell a change from a context that
	// was already in effect.
	before, _ := kubeconfig.CurrentContext(in.paths)

	if interrupted() {
		return result, errLogoutInterrupted
	}

	// Nothing has changed up to here. From here on, a failure is kept and the
	// rest goes on.
	save := in.save
	if save == nil {
		save = (*kubeconfig.Editor).Save
	}
	var failures []error
	var dangling []string
	static, keptToken, removedAny := false, false, false
	for i, ed := range editors {
		removal := ed.Remove(context)
		if !removal.Changed {
			continue
		}
		if err := save(ed); err != nil {
			failures = append(failures, fmt.Errorf("%s still holds %s: %w", files[i].path, context, err))
			continue
		}
		removedAny = true
		static = static || removal.StaticToken
		result.CurrentContextCleared = result.CurrentContextCleared || removal.ClearedCurrent
		result.Kubeconfigs = append(result.Kubeconfigs, files[i].path)
		if removal.KeptUserToken {
			keptToken = true
			result.warn("%s used the user %q in %s, which still holds a token. alpacon did not name that user, so it was left; remove it with 'kubectl config delete-user %s' if nothing else uses it.", context, removal.KeptUser, files[i].path, removal.KeptUser)
		}
		for _, name := range removal.Dangling {
			dangling = append(dangling, fmt.Sprintf("%q in %s", name, files[i].path))
		}
	}
	for _, key := range append(recordKeys(records), unreadable...) {
		if err := in.store.remove(key); err != nil {
			failures = append(failures, fmt.Errorf("the saved token for %s was not deleted: %w", in.cluster, err))
		}
	}
	// A staging file under a lock this command holds is a write that died, and
	// it can hold a token whether or not a record was ever renamed into place.
	for _, key := range locks.heldKeys() {
		if err := in.store.removeStaging(key); err != nil {
			failures = append(failures, fmt.Errorf("a partly written token for %s was not deleted: %w", in.cluster, err))
		}
	}
	result.Found = removedAny || len(records) > 0 || len(unreadable) > 0
	localDone.Store(true)

	// Past this point a server that stops answering must not keep kubectl, or
	// the next login of this cluster, locked out.
	locks.release()

	if after, err := kubeconfig.CurrentContext(in.paths); err == nil {
		result.EffectiveContext = after
		if result.CurrentContextCleared {
			result.currentUnset = after == ""
			if before == context && after != "" && after != context {
				result.warn("kubectl now uses the context %q, the current-context of another file in KUBECONFIG.", after)
			}
		}
	}

	if len(dangling) > 0 {
		result.warn("These contexts still name the removed cluster or user of %s: %s. Point them elsewhere with 'kubectl config set-context', or delete them.", in.cluster, strings.Join(dangling, ", "))
	}
	if len(unreadable) > 0 {
		result.warn("The token saved for %s could not be read, so it was deleted without closing its kube session.", in.cluster)
	}
	switch {
	case static:
		result.warn("The context held a static token, from 'alpacon kube config'. It is gone from the file, but its kube session stays open until its WorkSession ends.")
	case removedAny && len(records) == 0 && len(unreadable) == 0 && !keptToken:
		result.warn("No kube session is saved for %s, so none was closed; an open one stays open until its WorkSession ends.", in.cluster)
	}
	if len(records) > 0 && !removedAny && len(failures) == 0 && !in.explicit {
		result.warn("%s was not in %s. If you logged in with --kubeconfig, run this again with that file to remove the context.", context, strings.Join(in.paths, ", "))
	}

	skipClose := interrupted()
	for _, record := range records {
		if record.KubeSessionID == "" {
			continue
		}
		if skipClose {
			result.Sessions = append(result.Sessions, interruptedSession(result, record))
			continue
		}
		result.Sessions = append(result.Sessions, closeLoggedOutSession(result, in, record, watch.first))
	}
	return result, errors.Join(failures...)
}

// lockLogoutRecords takes the store lock of the key a login of this cluster runs
// under, record or not, so a login waiting on its first approval keeps out the
// logout too, and of each record an unlocked read picks. What is true under the
// locks is read again afterwards.
func lockLogoutRecords(in kubeLogoutInput, locks *logoutLocks, context string) error {
	if in.workspaceURL != "" {
		if err := locks.lockStore(in.store, loginKey(in)); err != nil {
			return err
		}
	}
	var ignored kubeLogoutResult
	_, _, picked, err := findLogoutTargets(&ignored, in, context)
	if err != nil {
		return err
	}
	for _, record := range picked {
		if err := locks.lockStore(in.store, record.key()); err != nil {
			return err
		}
	}
	return nil
}

// findLogoutTargets reads which kubeconfig files hold the context, the records
// saved for the cluster, and those the logout covers.
func findLogoutTargets(result *kubeLogoutResult, in kubeLogoutInput, context string) (files []logoutFile, saved, picked []kubeSessionRecord, err error) {
	files, err = scanKubeconfigs(result, in.paths, context)
	if err != nil {
		return nil, nil, nil, err
	}
	saved, err = in.store.records(in.cluster)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read the saved kube sessions: %w", err)
	}
	// The servers are those kubectl reaches through each definition of the
	// context, and those of the same-named clusters the logout deletes.
	var servers []string
	for _, t := range kubeconfig.ContextTargets(in.paths, context) {
		if t.Server != "" && !slices.Contains(servers, t.Server) {
			servers = append(servers, t.Server)
		}
	}
	for _, server := range kubeconfig.ClusterServers(in.paths, context) {
		if !slices.Contains(servers, server) {
			servers = append(servers, server)
		}
	}
	return files, saved, pickLogoutRecords(saved, servers, in.workspaceURL, in.explicit && len(files) == 0), nil
}

// scanKubeconfigs reads each file without taking its lock, so a file with
// nothing of the context is neither locked nor required to be writable. A file
// that cannot be parsed fails the logout only if it mentions the context:
// otherwise nothing is lost by leaving it alone.
func scanKubeconfigs(result *kubeLogoutResult, paths []string, context string) ([]logoutFile, error) {
	var files []logoutFile
	for _, path := range paths {
		h, err := kubeconfig.Inspect(path, context)
		if err != nil {
			data, readErr := os.ReadFile(path)
			if readErr == nil && !bytes.Contains(data, []byte(context)) {
				result.warn("Skipping %s: %s", path, err)
				continue
			}
			return nil, fmt.Errorf("cannot clean %s: %w", path, err)
		}
		if h.Entry || h.Current {
			files = append(files, logoutFile{path: path, holding: h})
		}
	}
	return files, nil
}

// pickLogoutRecords chooses the saved sessions the logout covers: the one behind
// each server the context or a deleted cluster points at, since the files can
// hold the same name for different workspaces. A server no record names, as after a hand edit of the
// server, falls back to the logged-in workspace's record. With no server to go
// by, it chooses as the credential plugin does without one, unless elsewhere is
// set: --kubeconfig named a file without the context, so the context, and the
// token's use, are somewhere else.
func pickLogoutRecords(records []kubeSessionRecord, servers []string, workspaceURL string, elsewhere bool) []kubeSessionRecord {
	if elsewhere {
		return nil
	}
	var picked []kubeSessionRecord
	add := func(r *kubeSessionRecord) {
		if r != nil && !slices.ContainsFunc(picked, func(p kubeSessionRecord) bool { return p.key() == r.key() }) {
			picked = append(picked, *r)
		}
	}
	if len(servers) == 0 {
		add(pickRecord(records, "", workspaceURL))
		return picked
	}
	for _, server := range servers {
		r := pickRecord(records, server, "")
		if r == nil && workspaceURL != "" {
			if i := slices.IndexFunc(records, func(r kubeSessionRecord) bool { return r.WorkspaceURL == workspaceURL }); i >= 0 {
				r = &records[i]
			}
		}
		add(r)
	}
	return picked
}

// lockRecords takes each record's store lock, if the first read did not find it,
// and reads the record under it: a login that finished since has replaced it,
// and its session is the one to close. A running login fails the logout before
// anything changes.
func lockRecords(store kubeSessionStore, locks *logoutLocks, picked []kubeSessionRecord) ([]kubeSessionRecord, error) {
	var records []kubeSessionRecord
	for _, record := range picked {
		if err := locks.lockStore(store, record.key()); err != nil {
			return nil, err
		}
		current, err := store.read(record.key())
		switch {
		case err != nil:
			records = append(records, record) // unreadable now, but still to be removed
		case current != nil:
			records = append(records, *current)
		}
	}
	return records, nil
}

// unreadableLoginRecord finds the logged-in workspace's record for the cluster
// when it is there but cannot be parsed. Nothing can use it, yet it can hold
// the token, so the logout deletes it too.
func unreadableLoginRecord(in kubeLogoutInput, records []kubeSessionRecord) []recordKey {
	if in.workspaceURL == "" {
		return nil
	}
	key := loginKey(in)
	if slices.Contains(recordKeys(records), key) {
		return nil
	}
	if _, err := in.store.read(key); err == nil {
		return nil
	}
	return []recordKey{key}
}

func loginKey(in kubeLogoutInput) recordKey {
	return recordKey{workspaceURL: in.workspaceURL, cluster: in.cluster}
}

func recordKeys(records []kubeSessionRecord) []recordKey {
	keys := make([]recordKey, 0, len(records))
	for _, r := range records {
		keys = append(keys, r.key())
	}
	return keys
}

// warnOtherRecords says which saved tokens of this cluster name the logout
// leaves, since they belong to other workspaces.
func warnOtherRecords(result *kubeLogoutResult, saved, picked []kubeSessionRecord) {
	for _, r := range saved {
		if slices.ContainsFunc(picked, func(p kubeSessionRecord) bool { return p.key() == r.key() }) {
			continue
		}
		result.warn("Kept the token saved for %s in workspace %s; log in to that workspace and run this again to remove it.", r.Cluster, r.WorkspaceURL)
	}
}

func interruptedSession(result *kubeLogoutResult, record kubeSessionRecord) loggedOutSession {
	result.warn("Interrupted before closing the kube session %s. It stays open until its WorkSession ends.", record.KubeSessionID)
	return loggedOutSession{ID: record.KubeSessionID, Workspace: record.WorkspaceURL, CloseError: "interrupted"}
}

// closeLoggedOutSession closes one session on the server. Every way this fails
// is a warning, since the local cleanup is done.
func closeLoggedOutSession(result *kubeLogoutResult, in kubeLogoutInput, record kubeSessionRecord, cancel <-chan struct{}) loggedOutSession {
	session := loggedOutSession{ID: record.KubeSessionID, Workspace: record.WorkspaceURL}
	if in.workspaceURL == "" || record.WorkspaceURL != in.workspaceURL {
		session.CloseError = "not logged in to the session's workspace"
		result.warn("The kube session %s belongs to %s, which you are not logged in to, so it was not closed. It stays open until its WorkSession ends.", record.KubeSessionID, record.WorkspaceURL)
		return session
	}
	err := in.closer(record, cancel)
	switch {
	case err == nil:
		session.Closed = true
	case errors.Is(err, errCallInterrupted):
		return interruptedSession(result, record)
	case utils.HTTPStatusCode(err) == http.StatusNotFound:
		session.NotFound = true
		result.warn("The server has no kube session %s that this login can see. It has ended, or another account opened it and it stays open until its WorkSession ends.", record.KubeSessionID)
	default:
		session.CloseError = err.Error()
		result.warn("Failed to close the kube session %s: %s. It stays open until its WorkSession ends.", record.KubeSessionID, err)
	}
	return session
}

// printKubeLogoutResult reports the result; complete is false when the logout
// failed partway and an error follows.
func printKubeLogoutResult(stdout io.Writer, result *kubeLogoutResult, complete bool) error {
	if utils.OutputFormat == utils.OutputFormatJSON {
		if err := utils.PrintJSONValue(stdout, result); err != nil {
			return utils.NewExitError("Failed to print the result: %s.", err)
		}
		return nil
	}
	if !result.Found {
		for _, line := range notFoundLines(result) {
			utils.CliInfo("%s", line)
		}
		return nil
	}
	if complete {
		utils.CliSuccess("Logged out of %s.", result.Cluster)
	}
	for _, path := range result.Kubeconfigs {
		utils.CliInfo("Removed %s from %s.", result.Context, path)
	}
	for _, session := range result.Sessions {
		if session.Closed {
			utils.CliInfo("Closed the kube session %s.", session.ID)
		}
	}
	if result.currentUnset {
		utils.CliInfo("current-context is now unset; pick one with 'kubectl config use-context NAME'.")
	}
	return nil
}

// notFoundLines is what a logout that removed nothing says.
func notFoundLines(result *kubeLogoutResult) []string {
	if result.keptElsewhere {
		return []string{fmt.Sprintf("Nothing was removed for %s.", result.Cluster)}
	}
	lines := []string{fmt.Sprintf("No kube login for %s was found.", result.Cluster)}
	if name, ok := strings.CutPrefix(result.Cluster, contextPrefix); ok && name != "" {
		lines = append(lines, fmt.Sprintf("CLUSTER is the cluster's name, without the %s prefix: alpacon kube logout %s", contextPrefix, shellQuote(name)))
	}
	return lines
}
