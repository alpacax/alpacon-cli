package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCommandOutputListener_HandleMessage_FiltersAndEmits(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantChunk *ChunkEvent // nil = expect no emission
	}{
		{
			name:      "matching command_output",
			payload:   `{"event_type":"command_output","payload":{"command_id":"cmd-1","seq":3,"content":"hi"}}`,
			wantChunk: &ChunkEvent{Seq: 3, Content: "hi"},
		},
		{
			name:    "wrong event_type",
			payload: `{"event_type":"server_status","payload":{"command_id":"cmd-1","seq":3,"content":"hi"}}`,
		},
		{
			name:    "wrong command_id",
			payload: `{"event_type":"command_output","payload":{"command_id":"cmd-OTHER","seq":3,"content":"hi"}}`,
		},
		{
			name:    "invalid json",
			payload: `not json`,
		},
		{
			name:    "empty payload",
			payload: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &CommandOutputListener{
				commandID: "cmd-1",
				chunks:    make(chan ChunkEvent, 1),
				done:      make(chan struct{}),
			}
			l.handleMessage([]byte(tt.payload))

			select {
			case got := <-l.chunks:
				if tt.wantChunk == nil {
					t.Fatalf("expected no emission, got %+v", got)
				}
				assert.Equal(t, *tt.wantChunk, got)
			case <-time.After(50 * time.Millisecond):
				if tt.wantChunk != nil {
					t.Fatal("expected emission but got nothing")
				}
			}
		})
	}
}
