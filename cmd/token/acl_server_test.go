package token

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseServerACLNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, single, bulk, wantSingle string
		wantNames                      []string
		wantErr                        string
	}{
		{name: "missing", wantErr: "one of --server or --servers is required"},
		{name: "blank single", single: " \t", wantErr: "one of --server or --servers is required"},
		{name: "padded single", single: " server ", wantSingle: "server"},
		{name: "padded bulk", bulk: " server ", wantNames: []string{"server"}},
		{name: "blank bulk", bulk: " , \t", wantErr: "--servers must contain at least one server name"},
		{name: "duplicates", bulk: " a, ,a, b ", wantNames: []string{"a", "a", "b"}},
		{name: "both", single: "a", bulk: "b", wantErr: "use either --server or --servers, not both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			single, names, err := parseServerACLNames(tc.single, tc.bulk)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSingle, single)
			assert.Equal(t, tc.wantNames, names)
		})
	}
}

func TestResolveServerIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		names, want []string
		wantErr     string
	}{
		{name: "empty", want: []string{}},
		{name: "whitespace", names: []string{" a ", "\tb"}, want: []string{"id-a", "id-b"}},
		{name: "duplicates", names: []string{"b", "a", "b"}, want: []string{"id-b", "id-a", "id-b"}},
		{name: "empty entry", names: []string{"a", " ", "b"}, want: []string{"id-a", "", "id-b"}, wantErr: "failed to resolve server ' ': server name is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []map[string]string{{"id": "id-" + r.URL.Query().Get("name")}}})
			}))
			defer ts.Close()
			ids, err := resolveServerIDs(&client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client()}, tc.names)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, ids)
		})
	}
}

func TestResolveServerIDsErrorOrder(t *testing.T) {
	laterFinished := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "first" {
			<-laterFinished
			time.Sleep(50 * time.Millisecond)
		} else {
			defer close(laterFinished)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"missing"}`))
	}))
	defer ts.Close()
	_, err := resolveServerIDs(&client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client()}, []string{"first", "second"})
	require.ErrorContains(t, err, "failed to resolve server 'first'")
}

func TestResolveServerIDsBoundsConcurrency(t *testing.T) {
	entered := make(chan struct{}, 3*serverResolveConcurrency)
	release := make(chan struct{})
	var active, maxActive atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := active.Add(1)
		defer active.Add(-1)
		for previous := maxActive.Load(); count > previous; previous = maxActive.Load() {
			if maxActive.CompareAndSwap(previous, count) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"id"}]}`))
	}))
	defer ts.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	names := make([]string, 3*serverResolveConcurrency)
	for i := range names {
		names[i] = fmt.Sprintf("server-%d", i)
	}
	done := make(chan error, 1)
	go func() {
		_, err := resolveServerIDs(&client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client()}, names)
		done <- err
	}()
	for range serverResolveConcurrency {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	select {
	case <-entered:
		t.Error("request exceeded concurrency bound")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("resolver did not finish")
	}
	assert.LessOrEqual(t, maxActive.Load(), int32(serverResolveConcurrency))
}
