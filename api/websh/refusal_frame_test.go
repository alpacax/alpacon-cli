package websh

import (
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

const pausedRefusalFrame = `{"type":"refusal","code":"session_paused","gate":"work_session","missing":"work_session",` +
	`"work_session_id":"11111111-1111-4111-8111-111111111111",` +
	`"console_url":"https://alpacon.io/acme/work-sessions/11111111-1111-4111-8111-111111111111",` +
	`"next":"/api/work-sessions/sessions/11111111-1111-4111-8111-111111111111/","hint":"untrusted server text"}`

const pausedNotice = "[Work Session 11111111-1111-4111-8111-111111111111 is paused after a critical denial. " +
	"Open the web console for the available recovery actions.]\r\n"

// Dropped input gets one refusal per keystroke; the terminal shows one notice
// for a run of them, and another only after output arrived in between.
func TestReadFromServer_PausedRefusalPrintsOneNoticePerRun(t *testing.T) {
	refusal := replayFrame{websocket.TextMessage, pausedRefusalFrame}

	_, stdout, stderr := readIdleFrames(t, refusal, refusal, replayFrame{websocket.BinaryMessage, "out"}, refusal)

	assert.Equal(t, "out", stdout, "the refusal is never terminal output")
	assert.Equal(t, pausedNotice+pausedNotice, stderr)
}

func TestReadFromServer_EmptyBinaryFrameKeepsPausedNoticeSuppressed(t *testing.T) {
	refusal := replayFrame{websocket.TextMessage, pausedRefusalFrame}

	_, stdout, stderr := readIdleFrames(t, refusal, replayFrame{websocket.BinaryMessage, ""}, refusal)

	assert.Empty(t, stdout)
	assert.Equal(t, pausedNotice, stderr)
}

// Another refusal code is not a pause, and its hint is never printed.
func TestReadFromServer_IgnoresAnotherRefusal(t *testing.T) {
	_, stdout, stderr := readIdleFrames(t, replayFrame{websocket.TextMessage, `{"type":"refusal","code":"other","hint":"untrusted server text"}`})

	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}
