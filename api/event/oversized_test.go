package event

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveOversized(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		platform  string
		oversized bool
		wantErr   bool
	}{
		{"at limit stays inline", strings.Repeat("a", 2048), "linux", false, false},
		{"one over limit is oversized", strings.Repeat("a", 2049), "linux", true, false},
		{"multibyte counts bytes", strings.Repeat("가", 683), "linux", true, false},
		{"windows oversized is an error", strings.Repeat("a", 2049), "windows", false, true},
		{"windows inline is fine", "dir", "windows", false, false},
		{"unknown platform rejected", strings.Repeat("a", 2049), "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOversized(tt.command, tt.platform)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.oversized, got)
		})
	}
}
