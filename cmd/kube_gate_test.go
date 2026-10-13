package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/kube"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeKubeGateConfig writes a config file under a fresh home and returns the
// home; a nil cfg leaves the home without one.
func writeKubeGateConfig(t *testing.T, cfg map[string]any) string {
	t.Helper()
	home := t.TempDir()
	if cfg == nil {
		return home
	}
	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600))
	return home
}

func runKubeGate(t *testing.T, home string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, "TestKubeGateHelperProcess", "kube-gate-helper", args,
		"GO_WANT_KUBE_GATE_HELPER=1", homeEnvVar()+"="+home)
}

func TestKubeGateBlocksWhenDisabled(t *testing.T) {
	t.Parallel()
	homes := []struct {
		name string
		cfg  map[string]any
	}{
		{name: "no config"},
		{name: "surface off", cfg: map[string]any{
			"workspace_url": "https://ws.example.com", "workspace_name": "ws", "token": "tok", "kubernetes_surface": false,
		}},
		{name: "surface absent", cfg: map[string]any{
			"workspace_url": "https://ws.example.com", "workspace_name": "ws", "token": "tok",
		}},
		{name: "surface on without credential", cfg: map[string]any{
			"workspace_url": "https://ws.example.com", "workspace_name": "ws", "kubernetes_surface": true,
		}},
	}
	invocations := [][]string{
		{"kube"},
		{"kube", "ls"},
		{"k8s", "ls"},
		{"clusters"},
		{"clusters", "describe", "prod"},
		{"kube", "--help"},
		{"kube", "ls", "--help"},
		{"kube", "-h"},
		{"help", "kube"},
		{"help", "kube", "ls"},
		{"kube", "describe"},
		{"kube", "bogus"},
		{"kube", "ls", "--bogus"},
		{"--output", "json", "kube", "ls"},
	}

	for _, h := range homes {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()
			home := writeKubeGateConfig(t, h.cfg)
			for _, args := range invocations {
				stdout, stderr, exitCode := runKubeGate(t, home, args...)

				assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "%v", args)
				assert.Contains(t, stderr, kube.NotEnabledMessage, "%v", args)
				assert.NotContains(t, stdout+stderr, "Usage:", "%v", args)
				assert.NotContains(t, stderr, "report on", "an expected refusal carries no issue footer: %v", args)
			}
		})
	}
}

func TestKubeGateRootHelp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cfg      map[string]any
		expected bool
	}{
		{name: "disabled hides kube", cfg: map[string]any{
			"workspace_url": "https://ws.example.com", "workspace_name": "ws", "token": "tok",
		}},
		{name: "enabled lists kube", cfg: map[string]any{
			"workspace_url": "https://ws.example.com", "workspace_name": "ws", "token": "tok", "kubernetes_surface": true,
		}, expected: true},
		{name: "enabled with access token lists kube", cfg: map[string]any{
			"workspace_url": "https://ws.example.com", "workspace_name": "ws", "access_token": "jwt", "kubernetes_surface": true,
		}, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			home := writeKubeGateConfig(t, tt.cfg)
			stdout, stderr, exitCode := runKubeGate(t, home, "--help")

			require.Equal(t, 0, exitCode, "stderr: %s", stderr)
			if tt.expected {
				assert.Regexp(t, `(?m)^\s+kube\s+View Kubernetes clusters`, stdout)
			} else {
				assert.NotRegexp(t, `(?m)^\s+kube\s`, stdout)
			}
		})
	}
}

func TestKubeGateEnabledReachesGroup(t *testing.T) {
	t.Parallel()
	home := writeKubeGateConfig(t, map[string]any{
		"workspace_url": "https://ws.example.com", "workspace_name": "ws", "token": "tok", "kubernetes_surface": true,
	})

	for _, args := range [][]string{{"help", "kube"}, {"k8s", "--help"}, {"clusters", "-h"}} {
		stdout, stderr, exitCode := runKubeGate(t, home, args...)
		assert.Equal(t, 0, exitCode, "%v stderr: %s", args, stderr)
		assert.Contains(t, stdout, "alpacon kube [command]", "%v", args)
		assert.Regexp(t, `(?m)^\s+ls\s+List Kubernetes clusters`, stdout, "%v", args)
		assert.Regexp(t, `(?m)^\s+describe\s+Show details of a Kubernetes cluster`, stdout, "%v", args)
	}

	_, stderr, exitCode := runKubeGate(t, home, "kube")
	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Contains(t, stderr, "a subcommand is required")
	assert.NotContains(t, stderr, kube.NotEnabledMessage)
}

// A stale yes meets a 404 on the cluster list, which reads as not enabled;
// a 404 on one cluster's detail is a cluster deleted after it was resolved.
func TestKubeGateMapsServerNotFound(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/kubernetes/clusters/" && r.URL.Query().Get("name") == "gone":
			_, _ = w.Write([]byte(`{"count":1,"current":1,"next":null,"previous":null,"last":1,"results":[{"id":"c-gone","name":"gone"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/kubernetes/"):
			// Every other path under /api/kubernetes/ answers as a server
			// with the surface turned off.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	home := writeKubeGateConfig(t, map[string]any{
		"workspace_url": ts.URL, "workspace_name": "ws", "token": "tok", "kubernetes_surface": true,
	})

	for _, args := range [][]string{{"kube", "ls"}, {"kube", "describe", "prod"}} {
		_, stderr, exitCode := runKubeGate(t, home, args...)
		assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "%v", args)
		assert.Contains(t, stderr, kube.NotEnabledMessage, "%v", args)
	}

	_, stderr, exitCode := runKubeGate(t, home, "kube", "describe", "gone")
	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Contains(t, stderr, "Failed to retrieve the cluster")
	assert.NotContains(t, stderr, kube.NotEnabledMessage)
}

func TestKubeGateHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_KUBE_GATE_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "kube-gate-helper")
	if !ok {
		os.Exit(2)
	}
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}
