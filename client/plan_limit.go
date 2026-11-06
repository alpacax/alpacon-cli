package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// legacyPlanLimitAxis maps a 402's "code" to its axis for a server that
// predates the "gate"/"axis" envelope. Deliberately excludes
// workspace_free_limit_exceeded: that gate-less 402 can also be a
// Free-workspace refusal, which this mapping must not repaint as a plan
// limit.
var legacyPlanLimitAxis = map[string]string{
	"server_limit_exceeded":      "server",
	"user_limit_exceeded":        "user",
	"application_limit_exceeded": "application",
	"websh_limit_exceeded":       "websh",
	"webftp_limit_exceeded":      "webftp",
	"websh_share_limit_exceeded": "websh-share",
}

// axisDisplayNames renders an axis for a human reader.
var axisDisplayNames = map[string]string{
	"server":      "servers",
	"user":        "users (pending invitations count)",
	"application": "applications",
	"websh":       "Websh hours this month",
	"webftp":      "WebFTP transfer volume this month",
	"websh-share": "Websh session sharing",
	"workspace":   "Free workspaces",
}

// rewritePlanLimitMessage replaces err's message with planLimitMessage's
// rendering when the 402 it carries classifies as a plan limit or a
// feature lock (client classification rules 1–3); rule 4 (any other 402)
// leaves the generic message parseAPIError already built untouched.
func (ac *AlpaconClient) rewritePlanLimitMessage(err error) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		return err
	}
	if message, ok := planLimitMessage(ae.axis, ae.code, ae.gate, ae.retryAfter, ac.BaseURL, ae.hint != ""); ok {
		ae.message = message
	}
	return err
}

// planLimitMessage implements the client classification and the CLI/MCP/alpamon
// message template for a 402 from the server:
//
//  1. gate=="plan" with axis present: a plan limit on axis.
//  2. gate absent and code is one of the six legacy *_limit_exceeded codes: a
//     plan limit, axis from legacyPlanLimitAxis (an older server, or an agent
//     talking to one, that predates "gate"/"axis").
//  3. gate=="plan" with no axis: a feature lock, not a limit.
//  4. Anything else: not a plan-limit response at all; ok is false and the
//     caller leaves today's message alone.
//
// With serverHint the server's hint, printed under this message, is the
// guidance, so the remedy sentences and the feature-lock sentence are left
// out. What the hint cannot carry stays: the limit reached, the reset day and
// where to upgrade.
func planLimitMessage(axis, code, gate string, retryAfter time.Duration, workspaceURL string, serverHint bool) (string, bool) {
	switch {
	case gate == gatePlan && axis != "":
		// rule 1: axis already carried by the modern envelope.
	case gate == "":
		mapped, known := legacyPlanLimitAxis[code]
		if !known {
			return "", false // rule 4
		}
		axis = mapped // rule 2
	case gate == gatePlan:
		// rule 3: a feature lock—no axis to name a limit on.
		lock := featureLockSentence
		if serverHint {
			lock = hintedRefusalMessage(http.StatusPaymentRequired, code) + "."
		}
		return fmt.Sprintf("%s Upgrade: %s. Talk to us: %s", lock, billingLocation(workspaceURL), talkToUsURL), true
	default:
		return "", false // rule 4: some other gate, e.g. role or token_scope
	}

	remedy := planLimitRemedy(axis, retryAfter)
	if serverHint {
		remedy = ""
		if retryAfter > 0 {
			remedy = monthlyRemedy(retryAfter)
		}
	}
	if remedy != "" {
		remedy += " "
	}
	message := fmt.Sprintf("plan limit reached: %s. %sUpgrade: %s. Talk to us: %s",
		axisDisplayName(axis), remedy, billingLocation(workspaceURL), talkToUsURL)
	return message, true
}

// axisDisplayName renders axis for a human reader, falling back to the raw
// value for an axis this CLI does not yet know (a future server addition).
func axisDisplayName(axis string) string {
	if name, ok := axisDisplayNames[axis]; ok {
		return name
	}
	return axis
}

// planLimitRemedy picks the count-cap or monthly-reset remedy sentence:
// retryAfter is only ever set on a monthly axis (websh, webftp, websh-share)
// and only when the limit is above zero, so its presence alone decides
// which sentence applies. axisServer gets an extra hint no other axis needs:
// a host cap is the one limit a stale, undeleted entry can trip by accident.
func planLimitRemedy(axis string, retryAfter time.Duration) string {
	remedy := countCapRemedy
	if retryAfter > 0 {
		remedy = monthlyRemedy(retryAfter)
	}
	if axis == axisServer {
		remedy += " " + serverAxisReregisterHint
	}
	return remedy
}

// monthlyRemedy renders "resets in N days" from the Retry-After delay,
// rounded to the nearest day and floored at one so a delay under twelve hours
// never reads as "0 days".
func monthlyRemedy(retryAfter time.Duration) string {
	days := max(int(retryAfter.Round(24*time.Hour)/(24*time.Hour)), 1)
	return fmt.Sprintf("Resets in %d days.", days)
}

// billingLocation is the console billing page for a workspace whose host is
// <label>.<region>.alpacon.io, or the words pointing there for any other host
// (self-hosted): a public repo never prints an internal or unverified URL.
func billingLocation(workspaceURL string) string {
	label, ok := consoleLabel(workspaceURL)
	if !ok {
		return selfHostedBillingWords
	}
	return fmt.Sprintf(consoleBillingURLFormat, label)
}

// consoleLabel extracts the workspace label from a canonical Alpacon Cloud
// host. It mirrors cmd/login.go's isCloudWorkspaceURL—the CLI's other
// definition of "this is Alpacon Cloud"—rather than sharing it: client cannot
// import cmd. The URL must round-trip, case-insensitively, to exactly
// https://<label>.<region>.alpacon.io; a different scheme, a port, a path, or
// a non-4-label host all fail this, so a self-hosted endpoint whose hostname
// merely resembles the pattern (or a cloud host spelled in another case)
// never gets an unverified—or missing—cloud billing link.
func consoleLabel(workspaceURL string) (string, bool) {
	raw := strings.TrimSpace(workspaceURL)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	parts := strings.Split(parsed.Hostname(), ".")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	if !strings.EqualFold(strings.Join(parts[2:], "."), "alpacon.io") {
		return "", false
	}
	canonical := fmt.Sprintf("https://%s.%s.alpacon.io", parts[0], parts[1])
	if !strings.EqualFold(strings.TrimSuffix(raw, "/"), canonical) {
		return "", false
	}
	return parts[0], true
}
