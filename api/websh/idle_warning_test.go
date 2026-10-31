package websh

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadFromServer_IdleWarningUsesStderrAndLocalTime(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("test", 9*60*60)
	t.Cleanup(func() { time.Local = local })
	closesAt := time.Date(time.Now().Year()+1, time.January, 2, 6, 4, 5, 0, time.UTC)

	_, stdout, stderr := readIdleFrames(t, idleWarningFrame(closesAt))

	assert.Empty(t, stdout)
	assert.Equal(t, "[Idle: this session disconnects at 15:04:05 unless you type something.]\r\n", stderr)
}

func TestReadFromServer_IdleWarningPreservesReplay(t *testing.T) {
	closesAt := time.Date(time.Now().Year()+1, time.January, 2, 15, 4, 5, 0, time.Local)
	warning := idleWarningFrame(closesAt)
	for _, tc := range []struct {
		name   string
		frames []replayFrame
		epoch  string
		offset uint64
		output bool
	}{
		{"before origin", []replayFrame{warning, {websocket.TextMessage, `{"type":"replay_origin","epoch":"epoch-1","offset":100}`}, {websocket.BinaryMessage, "hello"}}, "epoch-1", 105, true},
		{"after origin", []replayFrame{{websocket.TextMessage, `{"type":"replay_origin","epoch":"epoch-1","offset":100}`}, warning, {websocket.BinaryMessage, "hello"}}, "epoch-1", 105, true},
		{"without binary", []replayFrame{warning}, "", 0, false},
		{"legacy", []replayFrame{{websocket.BinaryMessage, "hello"}, warning, {websocket.TextMessage, `{"type":"replay_origin","epoch":"too-late","offset":999}`}}, "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, stdout, stderr := readIdleFrames(t, tc.frames...)
			assert.Equal(t, tc.epoch, conn.replay.epoch)
			assert.Equal(t, tc.offset, conn.replay.offset)
			assert.Equal(t, tc.output, conn.replay.outputWritten)
			if tc.output {
				assert.Equal(t, "hello", stdout)
			} else {
				assert.Empty(t, stdout)
			}
			assert.Equal(t, "[Idle: this session disconnects at 15:04:05 unless you type something.]\r\n", stderr)
		})
	}
}

func TestReadFromServer_IdleWarningIgnoresInvalidDeadlines(t *testing.T) {
	closesAt := time.Date(time.Now().Year()+1, time.January, 2, 15, 4, 5, 0, time.Local)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing", `{"type":"idle_warning"}`},
		{"null", `{"type":"idle_warning","closes_at":null}`},
		{"invalid", `{"type":"idle_warning","closes_at":"not-a-time"}`},
		{"past", idleWarningFrame(time.Now().Add(-time.Hour)).body},
		{"unknown type", `{"type":"future_control","closes_at":"` + closesAt.Format(time.RFC3339) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stdout, stderr := readIdleFrames(t,
				replayFrame{websocket.TextMessage, tc.body}, idleWarningFrame(closesAt),
			)
			assert.Empty(t, stdout)
			assert.Equal(t, "[Idle: this session disconnects at 15:04:05 unless you type something.]\r\n", stderr)
		})
	}
}

func TestReadFromServer_IdleWarningPrintsOncePerDeadline(t *testing.T) {
	first := time.Date(time.Now().Year()+1, time.January, 2, 15, 4, 5, 0, time.Local)
	second := first.Add(time.Minute)
	_, stdout, stderr := readIdleFrames(t,
		idleWarningFrame(first), idleWarningFrame(first), idleWarningFrame(second), idleWarningFrame(first),
	)
	assert.Empty(t, stdout)
	assert.Equal(t, "[Idle: this session disconnects at 15:04:05 unless you type something.]\r\n"+
		"[Idle: this session disconnects at 15:05:05 unless you type something.]\r\n", stderr)
}

func TestReadFromServer_IdleWarningStaysDeduplicatedOnReconnect(t *testing.T) {
	closesAt := time.Date(time.Now().Year()+1, time.January, 2, 15, 4, 5, 0, time.Local)
	_, stdout, notices := runReplaySession(t,
		replayRound{frames: []replayFrame{idleWarningFrame(closesAt)}, closeCode: websocket.CloseServiceRestart},
		replayRound{frames: []replayFrame{idleWarningFrame(closesAt)}},
	)
	assert.Empty(t, stdout)
	assert.Equal(t, "[Idle: this session disconnects at 15:04:05 unless you type something.]\r\n\r\nConnection lost, reconnecting...\r\n", notices)
}

func TestReadFromServer_IdleWarningReadOnlyOmitsTypingAdvice(t *testing.T) {
	closesAt := time.Date(time.Now().Year()+1, time.January, 2, 15, 4, 5, 0, time.Local)
	_, stdout, stderr := readIdleFramesWithState(t, nil, true,
		idleWarningFrame(closesAt), idleWarningFrame(closesAt),
	)

	assert.Empty(t, stdout)
	assert.Equal(t, "[Idle: this session disconnects at 15:04:05.]\r\n", stderr)
}

func TestReadFromServer_IdleWarningPrunesExpiredHistory(t *testing.T) {
	expired := time.Now().Add(-time.Hour).UTC()
	retained := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	next := retained.Add(time.Minute)
	history := map[time.Time]struct{}{expired: {}, retained: {}}

	conn, stdout, stderr := readIdleFramesWithState(t, history, false,
		idleWarningFrame(retained), idleWarningFrame(next), idleWarningFrame(expired),
	)

	assert.Empty(t, stdout)
	assert.Equal(t, fmt.Sprintf("[Idle: this session disconnects at %s unless you type something.]\r\n", next.Local().Format("15:04:05")), stderr)
	assert.NotContains(t, conn.idleWarnings, expired)
	assert.Contains(t, conn.idleWarnings, retained)
	assert.Contains(t, conn.idleWarnings, next)
	assert.Len(t, conn.idleWarnings, 2)
}

func idleWarningFrame(closesAt time.Time) replayFrame {
	return replayFrame{websocket.TextMessage, fmt.Sprintf(`{"type":"idle_warning","closes_at":%q,"message":"untrusted server text"}`, closesAt.Format(time.RFC3339))}
}

func readIdleFrames(t *testing.T, frames ...replayFrame) (*connection, string, string) {
	t.Helper()
	return readIdleFramesWithState(t, nil, false, frames...)
}

func readIdleFramesWithState(t *testing.T, history map[time.Time]struct{}, readOnly bool, frames ...replayFrame) (*connection, string, string) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = ws.Close() }()
		for _, frame := range frames {
			if !assert.NoError(t, ws.WriteMessage(frame.kind, []byte(frame.body))) {
				return
			}
		}
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	t.Cleanup(ts.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	require.NoError(t, err)
	conn := newConnection(ws)
	conn.idleWarnings = history
	conn.readOnly = readOnly
	t.Cleanup(conn.close)
	stdout, stderr := testutil.CaptureOutput(t, func() {
		awaitReturn(t, "warning reader did not finish", conn.readFromServer)
	})
	return conn, stdout, stderr
}
