package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webhookCreateHelperMarker = "webhook-create-helper"

func TestWebhookCreatePostsWithoutOwnerOrIAMLookup(t *testing.T) {
	t.Parallel()
	args := []string{
		"webhook", "create",
		"--name=my-webhook",
		"--url=https://example.com/hook",
		"--provider=custom",
		"--ssl-verify=false",
		"--enabled=false",
	}

	stdout, stderr, exitCode := runHelperProcess(t, "TestWebhookCreateHelperProcess", webhookCreateHelperMarker, args,
		homeEnvVar()+"="+t.TempDir())

	assert.Zero(t, exitCode, stderr)
	assert.NotContains(t, stdout+stderr, "owner")
	assert.Contains(t, stderr, "Webhook created: my-webhook")
}

func TestWebhookCreateRejectsRemovedOwnerFlag(t *testing.T) {
	t.Parallel()
	args := []string{
		"webhook", "create",
		"--name=my-webhook",
		"--url=https://example.com/hook",
		"--provider=custom",
		"--ssl-verify=false",
		"--enabled=false",
		"--owner=alice",
	}

	_, stderr, exitCode := runHelperProcess(t, "TestWebhookCreateHelperProcess", webhookCreateHelperMarker, args,
		homeEnvVar()+"="+t.TempDir())

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Contains(t, stderr, "unknown flag: --owner")
}

func TestWebhookCreateHelperProcess(t *testing.T) {
	args, ok := helperArgsAfter(os.Args, webhookCreateHelperMarker)
	if !ok {
		return
	}

	var requestCount atomic.Int32
	var gotMethod, gotPath string
	var gotBody map[string]any
	var decodeErr error
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		if r.Method != http.MethodPost || r.URL.Path != "/api/notifications/webhooks/" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		decodeErr = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"wh-1","name":"my-webhook"}`))
	}))
	defer ts.Close()
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "token", "", "", "", "", 0, false))
	os.Args = append([]string{"alpacon"}, args...)

	Execute()

	require.NoError(t, decodeErr)
	assert.Equal(t, int32(1), requestCount.Load())
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/notifications/webhooks/", gotPath)
	assert.Equal(t, map[string]any{
		"name":       "my-webhook",
		"url":        "https://example.com/hook",
		"provider":   "custom",
		"ssl_verify": false,
		"enabled":    false,
	}, gotBody)
}
