package ftp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchFromURLLimited stops at its limit, so a file far larger than the caller
// accepts is never read whole.
func TestFetchFromURLLimited(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 1<<20)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	// The subtests run in parallel, after this function returns.
	t.Cleanup(ts.Close)

	tests := []struct {
		name  string
		limit int64
		want  int
	}{
		{name: "larger file is cut at the limit", limit: 1025, want: 1025},
		{name: "smaller file is read whole", limit: 2 << 20, want: 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := fetchFromURLLimited(ts.Client(), ts.URL, tt.limit, 1)
			require.NoError(t, err)
			assert.Len(t, data, tt.want)
		})
	}
}
