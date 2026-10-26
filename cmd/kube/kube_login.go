package kube

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/kubernetes"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const opKubeLogin = "kube_login"

const closeTimeout = 5 * time.Second

// credentialPluginCommand is the program the kubeconfig has kubectl run for a
// token. It is looked up on PATH rather than pinned to this binary's path,
// which a package manager moves on every upgrade.
const credentialPluginCommand = "alpacon"

type kubeLoginFlagValues struct {
	workSession  string
	purpose      string
	scopes       []string
	duration     string
	waitApproval string
	kubeconfig   string
	proxyPort    int
}

var kubeLoginFlags kubeLoginFlagValues

var kubeLoginCmd = &cobra.Command{
	Use:   "login CLUSTER",
	Short: "Log in to a cluster for kubectl",
	Long: `Open a kube session on a cluster and write a context named alpacon-CLUSTER
into your kubeconfig, then make it the current context so kubectl, k9s, and
other clients use it right away.

The kube session runs under a WorkSession that targets the cluster with the
kube scope. With --work-session that session is used as given. Otherwise the
command reuses one of yours that fits: the one ALPACON_WORK_SESSION names, the
one an earlier login to this cluster used, or another session for the cluster
that is active or still waiting on approval. Only when none fits does it
create one, from --purpose, --scope and --duration, prompting for
any that is missing in an interactive shell. A session waiting on approval is
waited on for --wait-approval (default 5m). The active WorkSession is never
changed, so exec, websh, cp and tunnel keep the session they use.

The context runs 'alpacon kube credentials' for its token, so the token is
never written to the kubeconfig; alpacon must be on the PATH kubectl sees. If
your kubectl restricts credential plugins, allow alpacon. Logging in to the
same cluster again replaces the context and closes the kube session the
previous login opened.

The kubeconfig file is the one 'kubectl config' itself writes: --kubeconfig,
else the first existing file in KUBECONFIG (the last one listed when none
exists), else ~/.kube/config. Use --proxy-port to send kubectl through
'alpacon kube proxy' on that local port.`,
	Example: `  alpacon kube login prod-east
  alpacon kube login prod-east --purpose "debug crashlooping api pods" --scope kube,kube_exec --duration 2h
  alpacon kube login prod-east --work-session 3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47
  alpacon kube login prod-east --kubeconfig ~/.kube/alpacon --proxy-port 18443`,
	// Counted in runKubeLogin, so a wrong count is a usage error (exit 2)
	// like the flags, not Cobra's own exit 1.
	Args: cobra.ArbitraryArgs,
	Run:  runKubeLogin,
}

func init() {
	kubeLoginCmd.Flags().StringVar(&kubeLoginFlags.workSession, "work-session", "", "WorkSession ID to open the kube session under (default: one of yours that targets the cluster, or a new one)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginFlags.purpose, "purpose", "", "What you're doing and why, for a new WorkSession, as in 'work-session create' (required in non-interactive mode when one is created)")
	// --intent was the flag's first name; it still reaches --purpose.
	kubeLoginCmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "intent" {
			name = "purpose"
		}
		return pflag.NormalizedName(name)
	})
	kubeLoginCmd.Flags().StringSliceVar(&kubeLoginFlags.scopes, "scope", nil, "Scopes for a new WorkSession: kube, or kube,kube_exec to also exec, attach and port-forward into pods (kube_exec is refused for an AI agent)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginFlags.duration, "duration", "", "Lifetime of a new WorkSession, counted from activation (e.g. 30m, 2h)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginFlags.waitApproval, "wait-approval", "", "How long to wait for a WorkSession pending approval (default 5m)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginFlags.kubeconfig, "kubeconfig", "", "kubeconfig file to write (default: the file kubectl writes)")
	kubeLoginCmd.Flags().IntVar(&kubeLoginFlags.proxyPort, "proxy-port", 0, "route kubectl through 'alpacon kube proxy' on this local port")
}

type kubeLoginInput struct {
	cluster       string
	workSession   string // from --work-session, used as given
	envSession    string // from ALPACON_WORK_SESSION, used only when it fits
	purpose       string
	scopes        []string // nil until named
	duration      time.Duration
	waitTimeout   time.Duration
	proxyPort     int
	kubeconfig    string // the --kubeconfig file as an absolute path, when given
	requesterType string
}

// readKubeLoginInput validates the flags. changed reports whether a flag was
// set; env is the ALPACON_WORK_SESSION value.
func readKubeLoginInput(cluster string, flags kubeLoginFlagValues, changed func(string) bool, env string, detected aiagent.Detection) (kubeLoginInput, error) {
	in := kubeLoginInput{
		cluster:       strings.TrimSpace(cluster),
		workSession:   strings.TrimSpace(flags.workSession),
		purpose:       strings.TrimSpace(flags.purpose),
		waitTimeout:   utils.DefaultApprovalWaitTimeout,
		requesterType: "user",
	}
	if in.cluster == "" {
		return in, errors.New("CLUSTER must name a cluster")
	}
	if changed("work-session") && in.workSession == "" {
		return in, errors.New("--work-session must name a WorkSession ID")
	}
	if in.workSession == "" {
		in.envSession = strings.TrimSpace(env)
	}
	if detected.Detected() {
		in.requesterType = "agent"
	}

	if changed("purpose") && in.purpose == "" {
		return in, errors.New("--purpose cannot be empty")
	}
	if changed("scope") {
		scopes, err := parseKubeScopes(flags.scopes)
		if err != nil {
			return in, err
		}
		if err := worksession.ValidateAgentScopes(in.requesterType, scopes); err != nil {
			return in, fmt.Errorf("%w; this shell runs under the AI agent %s", err, detected.Describe())
		}
		in.scopes = scopes
	}
	if changed("duration") {
		d, err := utils.ParsePositiveDuration("--duration", flags.duration)
		if err != nil {
			return in, err
		}
		in.duration = d
	}
	if changed("wait-approval") {
		d, err := utils.ParsePositiveDuration("--wait-approval", flags.waitApproval)
		if err != nil {
			return in, err
		}
		in.waitTimeout = d
	}
	if changed("proxy-port") {
		if err := validatePort(flags.proxyPort); err != nil {
			return in, fmt.Errorf("--proxy-port %w", err)
		}
		in.proxyPort = flags.proxyPort
	}

	if in.workSession != "" && (changed("purpose") || changed("scope") || changed("duration")) {
		return in, errors.New("--purpose, --scope and --duration describe a new WorkSession and cannot be combined with --work-session, which names an existing one")
	}
	return in, nil
}

func runKubeLogin(cmd *cobra.Command, args []string) {
	if len(args) != 1 {
		utils.CliUsageErrorEnvelopeWithExit(opKubeLogin, "kube login takes one CLUSTER argument, not %d; see 'alpacon kube login --help'.", len(args))
	}
	in, err := readKubeLoginInput(args[0], kubeLoginFlags, cmd.Flags().Changed, os.Getenv(worksession.WorkSessionEnvVar), aiagent.FromEnvironment())
	if err != nil {
		utils.CliUsageErrorEnvelopeWithExit(opKubeLogin, "%s.", err)
	}

	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}
	// A kube session is opened only by a login with a person behind it; a
	// token is refused, so it must not first create a WorkSession and an
	// approval request nothing can use.
	if ac.AccessToken() == "" {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, nil, "Tokens issued by Alpacon (service or personal API token) cannot open a kube session; it needs %s.", utils.LoginWithoutTokenHint)
	}
	// The login the session is saved for. A logout or another login during
	// the approval wait replaces it, and then nothing is saved: a token a
	// logout should have removed must not appear after it.
	login, err := config.LoadConfig()
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to read the login: %s.", err)
	}

	path, err := kubeconfig.ResolvePath(kubeLoginFlags.kubeconfig)
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to find the kubeconfig: %s.", err)
	}
	if kubeLoginFlags.kubeconfig != "" {
		if in.kubeconfig, err = filepath.Abs(path); err != nil {
			utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to find the kubeconfig: %s.", err)
		}
	}
	// Probed before any WorkSession exists, so a locked, malformed or
	// unwritable file is found out while nothing has been asked of an
	// approver. The lock itself is taken again only after the approval wait,
	// which can take minutes kubectl should not spend locked out.
	probe, err := kubeconfig.Open(path)
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to open the kubeconfig: %s.", err)
	}
	_ = probe.Close()

	clusterID, err := kubernetes.GetClusterIDByName(ac, in.cluster)
	if err != nil {
		if kubernetes.IsSurfaceOff(err) {
			utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "%s", kubernetes.SurfaceNotEnabledMessage)
		}
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to find the cluster: %s.", err)
	}

	store, err := defaultKubeSessionStore()
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to find the home directory: %s.", err)
	}
	// Held until the process ends, approval wait included, so a second login
	// to the cluster cannot create a second WorkSession beside this one. The
	// kernel drops it however the process exits.
	key := recordKey{workspaceURL: ac.BaseURL, cluster: in.cluster}
	storeLock, err := store.lock(key)
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Cannot log in to %s: %s.", in.cluster, err)
	}
	defer func() { _ = storeLock.Release() }()
	previous, err := store.read(key)
	if err != nil {
		utils.CliWarning("Ignoring the saved kube session for %s: %s", in.cluster, err)
		previous = nil
	}

	choice := chooseWorkSession(ac, in, clusterID, previous)

	held, err := holdKubeconfig(path, func(msg string) { utils.CliErrorEnvelopeWithExit(opKubeLogin, nil, "%s", msg) })
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to open the kubeconfig: %s.", err)
	}
	defer held.done()

	opts := kubeConfigOptions{
		clusterName:   in.cluster,
		workSessionID: choice.id,
		kubeconfig:    path,
		proxyPort:     in.proxyPort,
		interrupted:   held.interrupt.fired.Load,
	}
	writer := kubeSessionWriter{
		exec: credentialPlugin(in.cluster),
		persist: func(session *kubernetes.KubeSessionResponse) (func() error, error) {
			record := kubeSessionRecord{
				Cluster:        in.cluster,
				ClusterID:      clusterID,
				WorkspaceName:  ac.WorkspaceName,
				WorkspaceURL:   ac.BaseURL,
				KubeSessionID:  session.ID,
				WorkSessionID:  choice.id,
				ChannelToken:   session.ChannelToken,
				TokenExpiredAt: session.TokenExpiredAt,
				ProxyURL:       session.ProxyURL,
				SavedAt:        time.Now().UTC(),
			}
			if err := config.WhileLoggedIn(login, func() error { return store.write(record) }); err != nil {
				if errors.Is(err, config.ErrLoginChanged) {
					return nil, errors.New("you logged out or logged in again while this login was running, so its kube session was not saved")
				}
				return nil, fmt.Errorf("failed to save the kube session: %w", err)
			}
			return func() error {
				return config.WhileLoggedIn(login, func() error { return store.restore(key, previous) })
			}, nil
		},
	}
	result, err := openKubeSession(ac, held.editor, opts, clusterID, writer)
	held.done()
	if err != nil {
		exitKubeLoginError(err, in.cluster, choice.id)
	}

	printKubeLoginResult(kubeLoginResult{
		kubeConfigResult:   *result,
		WorkSessionCreated: choice.created,
		Scopes:             choice.scopes,
	}, ac.Insecure, time.Now())

	// Last, so the round trip is not part of what the user waits on: the
	// previous login's kube session is no longer reachable from kubectl.
	closePreviousSession(ac, previous, result.KubeSessionID)
}

// exitKubeLoginError reports a failure to open or write the kube session.
// Every exit under --output json is an envelope, the gate refusal included.
func exitKubeLoginError(err error, clusterName, workSessionID string) {
	exitKubeSessionError(err, clusterName, workSessionID, func(err error, format string, args ...any) {
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, format, args...)
	})
}

func credentialPlugin(cluster string) *kubeconfig.ExecUser {
	return &kubeconfig.ExecUser{
		Command: credentialPluginCommand,
		Args:    []string{"kube", "credentials", "--cluster", cluster},
	}
}

// closePreviousSession closes the kube session an earlier login to the
// cluster in this workspace opened, now that the context no longer reaches it.
func closePreviousSession(ac *client.AlpaconClient, previous *kubeSessionRecord, current string) {
	if previous == nil || previous.KubeSessionID == "" || strings.EqualFold(previous.KubeSessionID, current) {
		return
	}
	// The context already works, so a server that stops answering must not
	// hold the command, or the store lock that keeps out the next login of
	// this cluster, past closeTimeout.
	done := make(chan error, 1)
	go func() { done <- kubernetes.CloseSession(ac, previous.KubeSessionID) }()
	select {
	case err := <-done:
		if err != nil {
			utils.CliWarning("Failed to close the previous kube session %s: %s. It stays open until its WorkSession ends.", previous.KubeSessionID, err)
		}
	case <-time.After(closeTimeout):
		utils.CliWarning("Closing the previous kube session %s took longer than %s; it stays open until its WorkSession ends.", previous.KubeSessionID, closeTimeout)
	}
}

type workSessionChoice struct {
	id      string
	created bool
	scopes  []string // as granted, when the command read the session
}

// sessionWant is what a reusable WorkSession must offer.
type sessionWant struct {
	clusterID     string
	requesterType string
	scopes        []string // nil accepts any cluster session
}

// fits reports whether ws targets the cluster for this requester with every
// wanted scope, and is neither ended nor past its expiry.
func (w sessionWant) fits(ws *wsapi.WorkSession, now time.Time) bool {
	if ws.RequesterType != "" && ws.RequesterType != w.requesterType {
		return false
	}
	if !wsapi.TargetsCluster(ws, w.clusterID) || !slices.Contains(ws.Scopes, worksession.ScopeKube) {
		return false
	}
	for _, s := range w.scopes {
		if !slices.Contains(ws.Scopes, s) {
			return false
		}
	}
	if wsapi.IsEndedStatus(ws.Status) {
		return false
	}
	// A session created with a duration has no expiry until it activates.
	return ws.ExpiresAt.IsZero() || ws.ExpiresAt.After(now)
}

// usable reports whether a kube session can open under ws now.
func (w sessionWant) usable(ws *wsapi.WorkSession, now time.Time) bool {
	if !w.fits(ws, now) || ws.Status != wsapi.StatusActive {
		return false
	}
	return ws.StartsAt == nil || !ws.StartsAt.After(now)
}

// waitable reports whether ws fits and is still waiting on approval, so the
// command waits it out instead of filing another.
func (w sessionWant) waitable(ws *wsapi.WorkSession, now time.Time) bool {
	return w.fits(ws, now) && (ws.Status == wsapi.StatusPending || ws.Status == wsapi.StatusApproved)
}

func chosen(ws *wsapi.WorkSession, created bool) workSessionChoice {
	return workSessionChoice{id: ws.ID, created: created, scopes: ws.Scopes}
}

// pickClusterSession picks from the caller's sessions for the cluster: one
// usable now first, else the newest still waiting on approval.
func pickClusterSession(sessions []wsapi.WorkSession, want sessionWant, now time.Time) (ws *wsapi.WorkSession, wait bool) {
	var waiting *wsapi.WorkSession
	for i := range sessions {
		s := &sessions[i]
		if want.usable(s, now) {
			return s, false
		}
		if want.waitable(s, now) {
			if waiting == nil || s.AddedAt.After(waiting.AddedAt) {
				waiting = s
			}
		}
	}
	return waiting, waiting != nil
}

// chooseWorkSession settles the WorkSession, waiting out an approval if one
// is pending. It exits on a refusal, with the code the refusal carries.
func chooseWorkSession(ac *client.AlpaconClient, in kubeLoginInput, clusterID string, previous *kubeSessionRecord) workSessionChoice {
	want := sessionWant{clusterID: clusterID, requesterType: in.requesterType, scopes: in.scopes}
	now := time.Now()

	// A named session is used as given, and the server judges it; one still
	// waiting on approval is waited on first, which is how the command the
	// wait message prints resumes. A failed read leaves the judging to the
	// server too.
	if in.workSession != "" {
		ws, err := wsapi.GetWorkSession(ac, in.workSession)
		if errors.Is(err, utils.ErrInvalidID) {
			utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Invalid --work-session: %s.", err)
		}
		if err != nil {
			return workSessionChoice{id: in.workSession}
		}
		if want.waitable(ws, now) {
			return chosen(waitForWorkSession(ac, in, ws), false)
		}
		return chosen(ws, false)
	}

	// ALPACON_WORK_SESSION usually names the server session exec and websh
	// run under, so it is used only when it fits the cluster.
	if in.envSession != "" {
		ws, err := wsapi.GetWorkSession(ac, in.envSession)
		switch {
		case err == nil && want.usable(ws, now):
			utils.CliInfo("Using WorkSession %s from %s.", utils.SanitizeTerminalText(ws.ID), worksession.WorkSessionEnvVar)
			return chosen(ws, false)
		case err == nil && want.waitable(ws, now):
			return chosen(waitForWorkSession(ac, in, ws), false)
		default:
			utils.CliInfo("%s names a WorkSession that does not serve %s; looking for one that does.", worksession.WorkSessionEnvVar, utils.SanitizeTerminalText(in.cluster))
		}
	}

	// The session the previous login used is one read, and a failed read only
	// means it is not reused. The active WorkSession is not a candidate: it
	// can only hold a server session.
	if previous != nil && previous.WorkSessionID != "" {
		if ws, err := wsapi.GetWorkSession(ac, previous.WorkSessionID); err == nil && want.usable(ws, now) {
			utils.CliInfo("Using WorkSession %s.", utils.SanitizeTerminalText(ws.ID))
			return chosen(ws, false)
		}
	}

	if ws, wait := findClusterSession(ac, want, now); ws != nil {
		if !wait {
			utils.CliInfo("Using WorkSession %s.", utils.SanitizeTerminalText(ws.ID))
			return chosen(ws, false)
		}
		return chosen(waitForWorkSession(ac, in, ws), false)
	}

	return createClusterWorkSession(ac, in, clusterID)
}

// findClusterSession looks through the caller's own sessions for the cluster,
// so a login cut short while its WorkSession waited on approval resumes that
// session instead of filing a second request. A failed lookup is not fatal;
// the command goes on to create a session.
func findClusterSession(ac *client.AlpaconClient, want sessionWant, now time.Time) (*wsapi.WorkSession, bool) {
	me, err := iam.GetCurrentUser(ac)
	if err != nil {
		utils.CliWarning("Could not look up your WorkSessions for the cluster: %s", err)
		return nil, false
	}
	// Only a session still able to run is wanted, so the ended ones a cluster
	// gathers with every login are left on the server.
	sessions, err := wsapi.GetClusterWorkSessions(ac, wsapi.ClusterSessionFilter{
		ClusterID:     want.clusterID,
		Status:        strings.Join([]string{wsapi.StatusActive, wsapi.StatusApproved, wsapi.StatusPending}, ","),
		RequesterType: want.requesterType,
		AssignedUser:  me.ID,
	})
	if err != nil {
		utils.CliWarning("Could not look up your WorkSessions for the cluster: %s", err)
		return nil, false
	}
	return pickClusterSession(sessions, want, now)
}

// createClusterWorkSession files a WorkSession that targets the cluster alone
// and waits for it to activate.
func createClusterWorkSession(ac *client.AlpaconClient, in kubeLoginInput, clusterID string) workSessionChoice {
	interactive := utils.IsInteractiveShell()
	purpose := in.purpose
	if purpose == "" {
		if !interactive {
			utils.CliUsageErrorEnvelopeWithExit(opKubeLogin, "No WorkSession for %s was found to reuse. Non-interactive mode requires --purpose, --scope and --duration to create one, or --work-session to name one.", in.cluster)
		}
		purpose = utils.PromptForRequiredInput("Purpose (what you're doing and why): ")
	}
	scopes := in.scopes
	if scopes == nil && in.requesterType == "agent" {
		// An agent session cannot hold kube_exec, which leaves one choice.
		scopes = []string{worksession.ScopeKube}
	}
	if scopes == nil {
		if !interactive {
			utils.CliUsageErrorEnvelopeWithExit(opKubeLogin, "Non-interactive mode requires --scope (kube or kube,kube_exec) to create a WorkSession.")
		}
		scopes = promptKubeScopes()
	}
	duration := in.duration
	if duration == 0 {
		if !interactive {
			utils.CliUsageErrorEnvelopeWithExit(opKubeLogin, "Non-interactive mode requires --duration (e.g. 2h) to create a WorkSession.")
		}
		duration = promptDuration()
	}

	session, err := wsapi.CreateWorkSession(ac, wsapi.WorkSessionCreateRequest{
		Description:   purpose,
		RequesterType: in.requesterType,
		Scopes:        scopes,
		Clusters:      []string{clusterID},
		Duration:      wsapi.FormatDuration(duration),
	})
	if err != nil {
		utils.HandleWorkSessionError(err, utils.OperationKube, in.cluster, config.ResolveAuthMethod(), "")
		if hint := worksession.TargetErrorHint(err); hint != "" {
			utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to create a WorkSession: %s.", hint)
		}
		utils.CliErrorEnvelopeWithExit(opKubeLogin, err, "Failed to create a WorkSession: %s.", err)
	}
	utils.CliInfo("WorkSession %s created for %s (status: %s).", utils.SanitizeTerminalText(session.ID), utils.SanitizeTerminalText(in.cluster), utils.SanitizeTerminalText(session.Status))

	if session.Status == wsapi.StatusActive {
		return chosen(session, true)
	}
	return chosen(waitForWorkSession(ac, in, session), true)
}

// waitForWorkSession waits for ws to activate, saying first how to come back
// to it, since Ctrl+C or the timeout leaves it waiting on approval.
func waitForWorkSession(ac *client.AlpaconClient, in kubeLoginInput, ws *wsapi.WorkSession) *wsapi.WorkSession {
	resume := resumeAction(in, ws.ID)
	utils.CliInfo("WorkSession %s is %s; waiting up to %s for it to become active. To stop and come back later, press Ctrl+C and run '%s'.",
		utils.SanitizeTerminalText(ws.ID), utils.SanitizeTerminalText(ws.Status), in.waitTimeout, utils.SanitizeTerminalText(resume.Command))
	final, err := worksession.WaitForApproval(ac, ws.ID, true, worksession.ApprovalPollInterval, in.waitTimeout)
	if err != nil {
		worksession.ExitOnWaitError(opKubeLogin, ws, resume, err)
	}
	if utils.OutputFormat != utils.OutputFormatJSON {
		worksession.PrintSessionAdvisories(final)
	}
	return final
}

// resumeAction is the command that comes back to the WorkSession id, writing
// the same kubeconfig file through the same local proxy as this run.
func resumeAction(in kubeLoginInput, id string) utils.NextAction {
	action := worksession.KubeLoginAction(shellQuote(in.cluster), id)
	if in.kubeconfig != "" {
		action.Command += " --kubeconfig " + shellQuote(in.kubeconfig)
	}
	if in.proxyPort != 0 {
		action.Command += fmt.Sprintf(" --proxy-port %d", in.proxyPort)
	}
	return action
}

var kubeScopeChoices = []struct {
	scopes []string
	label  string
}{
	{[]string{worksession.ScopeKube}, "kube             the Kubernetes API (get, apply, logs, ...)"},
	{[]string{worksession.ScopeKube, worksession.ScopeKubeExec}, "kube,kube_exec   also exec, attach and port-forward into pods"},
}

func promptKubeScopes() []string {
	fmt.Fprintln(os.Stderr, "Scopes for the WorkSession:")
	for i, c := range kubeScopeChoices {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, c.label)
	}
	for {
		answer := utils.PromptForRequiredInput(fmt.Sprintf("Select [1-%d]: ", len(kubeScopeChoices)))
		if n, err := strconv.Atoi(strings.TrimSpace(answer)); err == nil && n >= 1 && n <= len(kubeScopeChoices) {
			return kubeScopeChoices[n-1].scopes
		}
		if scopes, err := parseKubeScopes(utils.SplitAndTrim(answer, ",")); err == nil {
			return scopes
		}
		utils.CliWarning("Enter a number from 1 to %d, or kube or kube,kube_exec.", len(kubeScopeChoices))
	}
}

func promptDuration() time.Duration {
	for {
		d, err := utils.ParsePositiveDuration("duration", utils.PromptForRequiredInput("Duration (e.g. 1h, 2h): "))
		if err == nil {
			return d
		}
		utils.CliWarning("%s.", err)
	}
}

// kubeLoginResult is what 'kube login' reports. It never holds the token.
type kubeLoginResult struct {
	kubeConfigResult
	WorkSessionCreated bool     `json:"work_session_created"`
	Scopes             []string `json:"scopes,omitempty"`
}

func printKubeLoginResult(result kubeLoginResult, insecure bool, now time.Time) {
	printKubeConfigWarnings(&result.kubeConfigResult, insecure)

	if utils.OutputFormat == utils.OutputFormatJSON {
		if err := utils.PrintJSONValue(os.Stdout, result); err != nil {
			utils.CliErrorWithExit("Failed to print the result: %s.", err)
		}
		return
	}

	utils.CliSuccess("Context %q ready (session expires %s)", result.Context, formatExpiry(result.TokenExpiresAt, now))
	printKubeConfigNext(&result.kubeConfigResult)
}

// formatExpiry shows the time alone for an expiry later today, and the date
// with it otherwise.
func formatExpiry(t, now time.Time) string {
	local, today := t.Local(), now.Local()
	if local.Year() == today.Year() && local.YearDay() == today.YearDay() {
		return local.Format("15:04")
	}
	return local.Format("2006-01-02 15:04")
}
