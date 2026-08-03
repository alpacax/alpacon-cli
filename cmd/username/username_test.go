package username

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetUsernameErrorText(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantMatch  string
		wantDetail string // when set, the fallback must preserve the original error detail
	}{
		{"in_use mapped", errors.New(`{"code": "user_username_in_use", "source": ""}`), "already in use", ""},
		{"disallowed mapped", errors.New(`{"code": "user_username_disallowed"}`), "reserved", ""},
		{"invalid mapped", errors.New(`{"code": "user_username_invalid"}`), "lowercase letters", ""},
		{"unknown falls back", errors.New("some network failure"), "Failed to set username", "some network failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setUsernameErrorText(tt.err)
			assert.Contains(t, got, tt.wantMatch)
			if tt.wantDetail != "" {
				assert.Contains(t, got, tt.wantDetail)
			}
		})
	}
}

func TestUsernameGetCommand_StripsControlSequences(t *testing.T) {
	// JSON escapes it on the wire, so what reaches the printer is a real ESC byte.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"chae\u001b[2K\rroot"}`))
	}))
	defer ts.Close()

	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "token", "", "", "", "", 0, false))

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	usernameGetCmd.Run(usernameGetCmd, nil)
	_ = w.Close()
	os.Stdout = old
	got := <-done

	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\r")
	assert.Contains(t, got, "chae")
	assert.Contains(t, got, "root")
}
