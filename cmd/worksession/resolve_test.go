package worksession_test

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_Priority(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		envUUID  string
		cfgUUID  string
		expected string
	}{
		{"all empty", "", "", "", ""},
		{"only config", "", "", "uuid-cfg", "uuid-cfg"},
		{"only env", "", "uuid-env", "", "uuid-env"},
		{"only flag", "uuid-flag", "", "", "uuid-flag"},
		{"env wins over config", "", "uuid-env", "uuid-cfg", "uuid-env"},
		{"flag wins over env", "uuid-flag", "uuid-env", "", "uuid-flag"},
		{"flag wins over env and config", "uuid-flag", "uuid-env", "uuid-cfg", "uuid-flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)
			t.Setenv(worksession.WorkSessionEnvVar, tt.envUUID)
			require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
			if tt.cfgUUID != "" {
				require.NoError(t, config.SetActiveWorkSession(tt.cfgUUID))
			}

			got, err := worksession.Resolve(tt.flag)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestAnnounceIfActive_PrintsToStderr(t *testing.T) {
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	worksession.AnnounceIfActive("uuid-x")
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	assert.Contains(t, buf.String(), "Using work-session uuid-x")
}

func TestAnnounceIfActive_SilentWhenEmpty(t *testing.T) {
	r, w, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	worksession.AnnounceIfActive("")
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	assert.Equal(t, "", buf.String())
}
