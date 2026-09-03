package event

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderEvent_JSONIsOneLinePerFrameAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()
	raw := []byte("{\n  \"event_type\": \"work_session\",\n  \"payload\": {\n    \"sub_type\": \"approved\",\n    \"brand_new_field\": 7\n  }\n}")

	var buf bytes.Buffer
	require.NoError(t, renderEvent(&buf, raw, utils.OutputFormatJSON, "ws-uuid", time.Now()))

	out := buf.String()
	assert.Equal(t, 1, bytes.Count([]byte(out), []byte("\n")), "one frame must produce exactly one line")
	assert.JSONEq(t, string(raw), out)
	assert.Contains(t, out, `"brand_new_field":7`, "a field the CLI does not know must survive")

	// The assertions above are order-insensitive and would pass an unmarshal-into-map
	// regression that alphabetizes keys; this one would not.
	var want bytes.Buffer
	require.NoError(t, json.Compact(&want, raw))
	assert.Equal(t, want.String()+"\n", out, "JSON output must be an exact byte-for-byte compaction of raw, key order included")
}

func TestRenderEvent_TableHasFourFixedFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 13, 4, 5, 0, time.UTC)

	tests := []struct {
		name   string
		raw    string
		target string
		want   string
	}{
		{
			name:   "work session status event",
			raw:    `{"event_type":"work_session","payload":{"category":"status","sub_type":"approved"}}`,
			target: "ws-uuid",
			want:   "13:04:05\twork_session\tapproved\tws-uuid\n",
		},
		{
			name:   "payload without sub_type",
			raw:    `{"event_type":"command_output","payload":{"command_id":"cmd","seq":0,"content":"x"}}`,
			target: "cmd",
			want:   "13:04:05\tcommand_output\t-\tcmd\n",
		},
		{
			name:   "no target given",
			raw:    `{"event_type":"notification","payload":{"sub_type":"created"}}`,
			target: "",
			want:   "13:04:05\tnotification\tcreated\t-\n",
		},
		{
			name:   "empty payload",
			raw:    `{"event_type":"servers_commit","payload":{}}`,
			target: "",
			want:   "13:04:05\tservers_commit\t-\t-\n",
		},
		{
			name:   "format characters go and line breaks become spaces",
			raw:    `{"event_type":"work_session","payload":{"sub_type":"appro\u202eved\nnext"}}`,
			target: "ws-uuid",
			want:   "13:04:05\twork_session\tapproved next\tws-uuid\n",
		},
		{
			// The JSON escapes decode to a real ESC, tab, and BEL.
			name:   "escapes and control bytes are stripped from server fields",
			raw:    `{"event_type":"work_\u001b[31msession","payload":{"sub_type":"appro\tved\u0007"}}`,
			target: "ws-uuid",
			want:   "13:04:05\twork_session\tappro ved\tws-uuid\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, renderEvent(&buf, []byte(tt.raw), utils.OutputFormatTable, tt.target, now))
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestRenderEvent_InvalidJSONWritesNothing(t *testing.T) {
	t.Parallel()
	for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			err := renderEvent(&buf, []byte("not json at all"), format, "ws-uuid", time.Now())

			require.Error(t, err)
			// stdout must stay parseable, which is the whole point of the command.
			assert.Empty(t, buf.String())
		})
	}
}

// A bare JSON null unmarshals into the zero struct without error, so without the check
// the table path would print it as a legitimate-looking row of dashes.
func TestRenderEvent_TableRejectsAFrameWithNoEventType(t *testing.T) {
	t.Parallel()
	// The escape-only case pins that the check runs after stripping, not before.
	for _, raw := range []string{
		`null`,
		`{"payload":{"sub_type":"approved"}}`,
		`{"event_type":"\u001b[31m","payload":{}}`,
		`{"event_type":"\n","payload":{}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var buf bytes.Buffer
			err := renderEvent(&buf, []byte(raw), utils.OutputFormatTable, "ws-uuid", time.Now())

			require.Error(t, err)
			assert.Empty(t, buf.String())
		})
	}
}
