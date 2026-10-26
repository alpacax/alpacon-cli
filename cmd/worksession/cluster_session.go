package worksession

import (
	"errors"
	"fmt"
	"slices"

	"github.com/alpacax/alpacon-cli/utils"
)

// The scopes a cluster work session holds: kube for the Kubernetes API, and
// kube_exec on top of it for exec, attach and port-forward into pods.
const (
	ScopeKube     = "kube"
	ScopeKubeExec = "kube_exec"
)

// kubeScopes are the only scopes a cluster session may hold, and the ones a
// server session may not.
var kubeScopes = []string{ScopeKube, ScopeKubeExec}

// IsKubeScope reports whether scope belongs to a cluster session.
func IsKubeScope(scope string) bool {
	return slices.Contains(kubeScopes, scope)
}

// NormalizeClusterScopes checks a scope list for a cluster session and puts
// it in the order the server lists it. kube_exec implies kube, which every
// cluster session holds, so it is added rather than refused, the way --sudo
// adds the sudo scope; anything but kube and kube_exec is refused.
func NormalizeClusterScopes(scopes []string) ([]string, error) {
	var others []string
	for _, s := range scopes {
		if !IsKubeScope(s) {
			others = append(others, s)
		}
	}
	if len(others) > 0 {
		return nil, fmt.Errorf("a cluster session holds only kube and kube_exec, not %s; pass --scope kube or --scope kube,kube_exec", joinOrNone(others))
	}
	if len(scopes) == 0 {
		return nil, errors.New("a cluster session needs the kube scope; pass --scope kube or --scope kube,kube_exec")
	}
	normalized := []string{ScopeKube}
	if slices.Contains(scopes, ScopeKubeExec) {
		normalized = append(normalized, ScopeKubeExec)
	}
	return normalized, nil
}

func validateServerScopes(scopes []string) error {
	for _, s := range scopes {
		if IsKubeScope(s) {
			return fmt.Errorf("%q applies only to a session that targets a cluster; pass --cluster instead of --server", s)
		}
	}
	return nil
}

type clusterSessionFlags struct {
	accountSet bool
	sudoSet    bool
}

// validateClusterSession checks a cluster session's flags before any request
// and returns its scopes normalized. A kube session acts as the caller's own
// user name, so there is no account to name, and sudo has no meaning on a
// cluster.
func validateClusterSession(scopes []string, flags clusterSessionFlags) ([]string, error) {
	if flags.accountSet {
		return nil, errors.New("--account cannot be combined with --cluster; a kube session acts as your own user name")
	}
	if flags.sudoSet {
		return nil, errors.New("--sudo cannot be combined with --cluster")
	}
	return NormalizeClusterScopes(scopes)
}

// TargetErrorHint says what to do about the server's refusal of a work
// session's targets, scopes or lifetime, or returns "" for any other error.
func TargetErrorHint(err error) string {
	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case utils.WorkSessionMixedTargets:
		return "a work session targets servers or one cluster, never both"
	case utils.WorkSessionNoServers:
		return "a work session needs a target; pass --server or --cluster"
	case utils.WorkSessionTooManyClusters:
		return "a work session targets one cluster at most"
	case utils.WorkSessionInvalidScope:
		return "the scopes do not fit the target: a cluster session holds kube and optionally kube_exec, and a server session holds neither"
	case utils.ClusterNotFound:
		return "no cluster you can reach has that name; check 'alpacon kube ls'"
	case utils.WorkSessionTargetAccountNotApplicable:
		return "a cluster session acts as your own user name, so it takes no account"
	case utils.WorkSessionExpiresExceedsMaxTTL:
		return "the requested lifetime is longer than this workspace allows; ask for a shorter one"
	}
	return ""
}
