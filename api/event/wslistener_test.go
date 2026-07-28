package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWSListener_StartPanicsWithoutHandleFrame(t *testing.T) {
	w := newWSListener(nil, "", 0)

	assert.PanicsWithValue(t, "event: wsListener.handleFrame must be assigned before Start", func() {
		w.Start()
	})
}
