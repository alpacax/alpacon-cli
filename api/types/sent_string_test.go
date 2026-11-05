package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sentHolder struct {
	Cause SentString `json:"cause,omitzero"`
}

func TestSentStringRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		wire string
		want SentString
	}{
		{name: "absent key is omitted", wire: `{}`},
		{name: "null stays null", wire: `{"cause":null}`, want: SentString{Sent: true}},
		{name: "value is kept", wire: `{"cause":"risk_denied"}`, want: SentString{Value: "risk_denied", Sent: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got sentHolder
			require.NoError(t, json.Unmarshal([]byte(tc.wire), &got))
			assert.Equal(t, tc.want, got.Cause)

			raw, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, tc.wire, string(raw))
		})
	}
}

func TestSentStringRejectsNonString(t *testing.T) {
	t.Parallel()
	var got sentHolder

	require.Error(t, json.Unmarshal([]byte(`{"cause":1}`), &got))
}
