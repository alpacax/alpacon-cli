package mfa

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestCheckMFACompletion_Completed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, mfaCompletionURL, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"completed": true}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	completed, err := CheckMFACompletion(ac)
	assert.NoError(t, err)
	assert.True(t, completed)
}

func TestCheckMFACompletion_NotCompleted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"completed": false}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	completed, err := CheckMFACompletion(ac)
	assert.NoError(t, err)
	assert.False(t, completed)
}

func TestCheckSensitiveMFACompletion(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			// The regression: a proof inside the ordinary window but outside
			// the sensitive one. Reading "completed" here stops the poll while
			// the sensitive gate still rejects, so the action fails and only
			// the retry succeeds.
			name: "ordinary fresh, sensitive stale",
			body: `{"completed": true, "completed_sensitive": false}`,
			want: false,
		},
		{
			name: "both fresh",
			body: `{"completed": true, "completed_sensitive": true}`,
			want: true,
		},
		{
			// A server predating the two-tier split sends one verdict for both
			// tiers; falling back to it beats polling until the timeout.
			name: "server omits the sensitive verdict",
			body: `{"completed": true}`,
			want: true,
		},
		{
			name: "server omits the sensitive verdict and presence is stale",
			body: `{"completed": false}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, mfaCompletionURL, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{
				HTTPClient: ts.Client(),
				BaseURL:    ts.URL,
			}

			completed, err := CheckSensitiveMFACompletion(ac)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, completed)
		})
	}
}

func TestCheckMFACompletion_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	_, err := CheckMFACompletion(ac)
	assert.Error(t, err)
}
