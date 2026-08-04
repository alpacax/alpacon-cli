package event

import (
	"testing"
	"time"

	eventapi "github.com/alpacax/alpacon-cli/api/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkSessionStatusSubType(t *testing.T) {
	// The event channel and the REST projection agree everywhere except active.
	// Reusing the REST status verbatim would silently never match an approval.
	tests := []struct {
		status string
		want   string
	}{
		{status: "active", want: "activated"},
		{status: "pending", want: "pending"},
		{status: "approved", want: "approved"},
		{status: "rejected", want: "rejected"},
		{status: "expired", want: "expired"},
		{status: "revoked", want: "revoked"},
		{status: "cancelled", want: "cancelled"},
		{status: "completed", want: "completed"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			assert.Equal(t, tt.want, workSessionStatusSubType(tt.status))
		})
	}
}

func TestResolveWaitOptions_RegisteredTypeGetsItsDefaults(t *testing.T) {
	opts, err := resolveWaitOptions(nil, "work_session", "ws-uuid", nil, time.Minute)

	require.NoError(t, err)
	assert.Equal(t, []string{"approved", "activated"}, opts.OK)
	assert.Equal(t, []string{"rejected", "expired", "revoked", "cancelled", "completed"}, opts.Fail)
	assert.NotNil(t, opts.CatchUp, "a registered type with a target must get catch-up")
	assert.Equal(t, time.Minute, opts.Timeout)
}

func TestResolveWaitOptions_NoTargetMeansNoCatchUp(t *testing.T) {
	// Catch-up reads one resource by id; without a target there is nothing to read.
	opts, err := resolveWaitOptions(nil, "work_session", "", nil, time.Minute)

	require.NoError(t, err)
	assert.Nil(t, opts.CatchUp)
}

func TestResolveWaitOptions_UntilOverridesButKeepsCatchUp(t *testing.T) {
	opts, err := resolveWaitOptions(nil, "work_session", "ws-uuid", []string{"activated"}, time.Minute)

	require.NoError(t, err)
	assert.Equal(t, []string{"activated"}, opts.OK)
	assert.Empty(t, opts.Fail, "an explicit --until makes every listed sub type a success")
	assert.NotNil(t, opts.CatchUp, "overriding the end condition does not remove the race fix")
}

func TestResolveWaitOptions_UnregisteredTypeNeedsUntil(t *testing.T) {
	opts, err := resolveWaitOptions(nil, "notification", "", []string{"created"}, time.Minute)

	require.NoError(t, err)
	assert.Equal(t, []string{"created"}, opts.OK)
	assert.Nil(t, opts.CatchUp)

	_, err = resolveWaitOptions(nil, "notification", "", nil, time.Minute)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--until is required")
}

func TestResolveWaitOptions_OptionsAreEventAPITyped(t *testing.T) {
	// Guards the seam: cmd owns the domain knowledge, api/event stays type-ignorant.
	var opts eventapi.WaitOptions
	opts, err := resolveWaitOptions(nil, "work_session", "ws-uuid", nil, time.Minute)
	require.NoError(t, err)
	assert.NotEmpty(t, opts.OK)
}
