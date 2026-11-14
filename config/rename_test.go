package config

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryConfigRename(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		failures  int
		transient bool
		calls     int
		elapsed   time.Duration
	}{
		{"success", 0, true, 1, 0},
		{"sharing failure clears", 2, true, 3, 400 * time.Millisecond},
		{"sharing failure persists", 9, true, 6, time.Second},
		{"other error", 2, false, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				calls := 0
				err := retryConfigRename(func() error {
					calls++
					if calls <= tc.failures {
						return assert.AnError
					}
					return nil
				}, func(error) bool { return tc.transient })
				if tc.failures >= tc.calls {
					require.ErrorIs(t, err, assert.AnError)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, tc.calls, calls)
				assert.Equal(t, tc.elapsed, time.Since(start))
			})
		})
	}
}
