package authority

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorityCreationNeedsNoOwner(t *testing.T) {
	args := flag.Args()
	if len(args) == 2 && args[0] == "authority-create-helper" {
		ac := &client.AlpaconClient{HTTPClient: httpclient.New(false), BaseURL: args[1]}
		request, err := promptForAuthority(ac)
		require.NoError(t, err)
		response, err := cert.CreateAuthority(ac, request)
		require.NoError(t, err)
		require.Equal(t, "ca-1", response.ID)
		return
	}
	for _, defaults := range []bool{true, false} {
		name := "explicit validity"
		values := []string{"Root CA", "Example", "example.com", "4000", "400", "800", "my-server"}
		expected := `{"name":"Root CA","organization":"Example","domain":"example.com","root_valid_days":4000,"default_valid_days":400,"max_valid_days":800,"agent":"server-1","install":false}`
		if defaults {
			name = "default validity"
			values[3], values[4], values[5] = "", "", ""
			expected = `{"name":"Root CA","organization":"Example","domain":"example.com","root_valid_days":3650,"default_valid_days":365,"max_valid_days":730,"agent":"server-1","install":false}`
		}
		t.Run(name, func(t *testing.T) {
			var paths []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /api/servers/servers/":
					assert.Equal(t, "my-server", r.URL.Query().Get("name"))
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-1","name":"my-server"}]}`))
				case "POST /api/cert/authorities/":
					var body json.RawMessage
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					assert.JSONEq(t, expected, string(body))
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"ca-1","instruction":"install"}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthorityCreationNeedsNoOwner$", "--", "authority-create-helper", ts.URL)
			stdin, err := child.StdinPipe()
			require.NoError(t, err)
			stderr, err := child.StderrPipe()
			require.NoError(t, err)
			var stdout bytes.Buffer
			child.Stdout = &stdout
			require.NoError(t, child.Start())
			t.Cleanup(func() { _ = child.Process.Kill() })
			prompts := []string{
				"Common name for the CA. (e.g., AlpacaX Root CA): ",
				"Organization name that this CA belongs to. (e.g., AlpacaX): ",
				"Domain name of the root certificate: ",
				"Root certificate validity in days (default: 3650): ",
				"Child certificate validity in days (default: 365): ",
				"Maximum valid days that users can request (default: 730): ",
				"Name of server to run this CA on: ",
			}
			for i, prompt := range prompts {
				got := make([]byte, len(prompt))
				_, err := io.ReadFull(stderr, got)
				require.NoError(t, err)
				require.Equal(t, prompt, string(got))
				_, err = io.WriteString(stdin, values[i]+"\n")
				require.NoError(t, err)
			}
			require.NoError(t, stdin.Close())
			remaining, err := io.ReadAll(stderr)
			require.NoError(t, err)
			err = child.Wait()
			require.NoError(t, err, "stdout: %s; stderr: %s", stdout.String(), remaining)
			assert.NotContains(t, string(remaining), "Owner")
			assert.Equal(t, []string{"GET /api/servers/servers/", "POST /api/cert/authorities/"}, paths)
		})
	}
}
