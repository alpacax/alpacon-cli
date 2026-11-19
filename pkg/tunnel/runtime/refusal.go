package runtime

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/utils"
)

// emitPaused signals a paused session off the smux reading goroutine, which an
// OS notification would otherwise hold up.
var emitPaused = func(fields localsignal.Fields, ac *client.AlpaconClient) {
	go utils.EmitLocalSignal(localsignal.Paused, fields, ac)
}

// tunnelRefusalHandler reads the text frames of the tunnel socket: a paused
// session's refusal of a new stream signals the pause.
func tunnelRefusalHandler(ac *client.AlpaconClient) func([]byte) {
	return func(text []byte) {
		if fields, ok := utils.PausedRefusalFrame(text); ok {
			emitPaused(fields, ac)
		}
	}
}
