package worksession

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStatusFilter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"all clears filter", "all", ""},
		{"all is case-insensitive", "ALL", ""},
		{"active passes through", "active", "active"},
		{"comma multi-value passes through", "active,completed", "active,completed"},
		{"empty passes through", "", ""},
		{"surrounding whitespace trimmed", " active ", "active"},
		{"whitespace around all still clears", " all ", ""},
		{"whitespace-only becomes empty", "   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveStatusFilter(tt.input))
		})
	}
}

func TestResolveAssignedUser(t *testing.T) {
	const myID = "6eaa827d-616a-4fa9-ad42-4fbb67bb007b"

	t.Run("all lists everyone without resolving current user", func(t *testing.T) {
		calls := 0
		got, err := resolveAssignedUser("all", func() (string, error) { calls++; return myID, nil })
		require.NoError(t, err)
		assert.Equal(t, "", got)
		assert.Equal(t, 0, calls, "current user must not be resolved for --user all")
	})

	t.Run("all is case-insensitive", func(t *testing.T) {
		calls := 0
		got, err := resolveAssignedUser("ALL", func() (string, error) { calls++; return myID, nil })
		require.NoError(t, err)
		assert.Equal(t, "", got)
		assert.Equal(t, 0, calls)
	})

	t.Run("empty resolves to current user", func(t *testing.T) {
		calls := 0
		got, err := resolveAssignedUser("", func() (string, error) { calls++; return myID, nil })
		require.NoError(t, err)
		assert.Equal(t, myID, got)
		assert.Equal(t, 1, calls)
	})

	t.Run("uuid passes through without resolving current user", func(t *testing.T) {
		calls := 0
		got, err := resolveAssignedUser(myID, func() (string, error) { calls++; return "other", nil })
		require.NoError(t, err)
		assert.Equal(t, myID, got)
		assert.Equal(t, 0, calls)
	})

	t.Run("surrounding whitespace trimmed off uuid", func(t *testing.T) {
		got, err := resolveAssignedUser("  "+myID+"  ", func() (string, error) { return "other", nil })
		require.NoError(t, err)
		assert.Equal(t, myID, got)
	})

	t.Run("whitespace-only resolves to current user", func(t *testing.T) {
		calls := 0
		got, err := resolveAssignedUser("   ", func() (string, error) { calls++; return myID, nil })
		require.NoError(t, err)
		assert.Equal(t, myID, got)
		assert.Equal(t, 1, calls, "blank input must be treated as empty (self)")
	})

	t.Run("current user resolution error propagates", func(t *testing.T) {
		got, err := resolveAssignedUser("", func() (string, error) { return "", errors.New("boom") })
		require.Error(t, err)
		assert.Equal(t, "", got)
	})
}
