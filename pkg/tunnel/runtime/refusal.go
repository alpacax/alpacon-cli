package runtime

import (
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/utils"
)

const tunnelPauseNoticeInterval = time.Minute

var emitPaused = func(fields localsignal.Fields, ac *client.AlpaconClient) {
	utils.EmitLocalSignal(localsignal.Paused, fields, ac)
}

// tunnelRefusalHandler reads the text frames of the tunnel socket: a paused
// session's refusal of a new stream signals the pause.
func tunnelRefusalHandler(ac *client.AlpaconClient) func([]byte) {
	var nextNotice time.Time
	inFlight := make(chan struct{}, 1)
	return func(text []byte) {
		now := time.Now()
		if now.Before(nextNotice) {
			return
		}
		fields, ok := utils.PausedRefusalFrame(text)
		if !ok {
			return
		}
		select {
		case inFlight <- struct{}{}:
		default:
			return
		}
		nextNotice = now.Add(tunnelPauseNoticeInterval)
		emit := emitPaused
		go func() {
			defer func() { <-inFlight }()
			emit(fields, ac)
		}()
	}
}
