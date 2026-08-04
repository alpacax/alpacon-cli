package event

import (
	"fmt"
	"time"

	eventapi "github.com/alpacax/alpacon-cli/api/event"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
)

const (
	eventTypeWorkSession = "work_session"

	// The one place the event channel's vocabulary and the REST projection disagree:
	// the channel publishes activated where the resource reports active.
	workSessionActiveStatus   = "active"
	workSessionActivatedEvent = "activated"
)

// Sub types confirmed against alpacon-server 2cafc0f67 by reading every
// publish_work_session_status_change call site.
var waitAdapters = map[eventapi.EventType]waitAdapter{
	eventTypeWorkSession: {
		ok:      []string{"approved", workSessionActivatedEvent},
		fail:    []string{"rejected", "expired", "revoked", "cancelled", "completed"},
		catchUp: workSessionCatchUp,
	},
}

// waitAdapter is the per-type knowledge event wait needs. Types with no entry are
// driven entirely by --until, which is why an unknown type still works.
type waitAdapter struct {
	ok      []string
	fail    []string
	catchUp func(ac *client.AlpaconClient, targetID string) eventapi.CatchUpFunc
}

// workSessionStatusSubType translates a REST status into the event channel's vocabulary
// so one set of conditions covers both paths.
func workSessionStatusSubType(status string) string {
	if status == workSessionActiveStatus {
		return workSessionActivatedEvent
	}
	return status
}

func workSessionCatchUp(ac *client.AlpaconClient, targetID string) eventapi.CatchUpFunc {
	return func() (string, error) {
		session, err := wsapi.GetWorkSession(ac, targetID)
		if err != nil {
			return "", err
		}
		return workSessionStatusSubType(session.Status), nil
	}
}

// resolveWaitOptions assembles the conditions for one wait. until replaces a registered
// type's built-in end condition and is the only source for an unregistered one.
func resolveWaitOptions(ac *client.AlpaconClient, eventType eventapi.EventType, targetID string, until []string, timeout time.Duration) (eventapi.WaitOptions, error) {
	adapter, registered := waitAdapters[eventType]

	opts := eventapi.WaitOptions{Timeout: timeout}
	switch {
	case len(until) > 0:
		opts.OK = until
	case registered:
		opts.OK, opts.Fail = adapter.ok, adapter.fail
	default:
		return eventapi.WaitOptions{}, fmt.Errorf("--until is required for %s: this CLI has no built-in end condition for it", eventType)
	}

	// Kept even when --until overrides the condition: the subscribe/publish race is
	// independent of which sub types the caller cares about.
	if registered && adapter.catchUp != nil && targetID != "" {
		opts.CatchUp = adapter.catchUp(ac, targetID)
	}

	return opts, nil
}
