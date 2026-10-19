package csr

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	certApi "github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signRequestBody sends signRequest through the real client and returns the
// JSON body the server received.
func signRequestBody(t *testing.T, signRequest certApi.SignRequest) string {
	t.Helper()
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new-csr-id"}`))
	}))
	t.Cleanup(server.Close)
	ac := &client.AlpaconClient{HTTPClient: server.Client(), BaseURL: server.URL, Token: "test-token"}
	_, err := certApi.CreateSignRequest(ac, signRequest)
	require.NoError(t, err)

	return string(<-bodies)
}

func TestNewSignRequestBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		domains   string
		ips       string
		validDays int
		want      string
	}{
		{
			name:    "omitted flag leaves valid_days to the CA",
			domains: "a.com, b.com",
			want:    `{"domain_list":["a.com","b.com"],"ip_list":[]}`,
		},
		{
			name:      "explicit flag is sent",
			domains:   "a.com, b.com",
			validDays: 90,
			want:      `{"domain_list":["a.com","b.com"],"ip_list":[],"valid_days":90}`,
		},
		{
			name: "IP-only request fills ip_list",
			ips:  "10.0.0.1",
			want: `{"domain_list":[],"ip_list":["10.0.0.1"]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := signRequestBody(t, newSignRequest(tt.domains, tt.ips, tt.validDays))
			assert.JSONEq(t, tt.want, got)
		})
	}
}

func TestParseValidDaysInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "blank leaves it to the CA", input: "", want: 0},
		{name: "whitespace leaves it to the CA", input: "  ", want: 0},
		{name: "positive number", input: " 90 ", want: 90},
		{name: "zero refused", input: "0", wantErr: true},
		{name: "negative refused", input: "-1", wantErr: true},
		{name: "text refused", input: "ninety", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseValidDaysInput(tt.input)
			if tt.wantErr {
				require.EqualError(t, err, "valid days must be a positive integer")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// createError returns the error CreateSignRequest gives back through the real
// client when the server answers 400 with body.
func createError(t *testing.T, body string) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	ac := &client.AlpaconClient{HTTPClient: server.Client(), BaseURL: server.URL, Token: "test-token"}
	_, err := certApi.CreateSignRequest(ac, certApi.SignRequest{DomainList: []string{"a.com"}})
	require.Error(t, err)
	return err
}

func TestCreateSignRequestErrorText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		body      string
		transport error
		validDays int
		// want is the whole message; empty means the error is passed through
		// behind the generic prefix.
		want string
	}{
		{
			name:      "invalid period with explicit valid days",
			body:      `{"code":"cert_invalid_period"}`,
			validDays: 900,
			want: "The CA refused a validity of 900 days: it must be between 1 and the CA's maximum " +
				"(Max Valid Days in 'alpacon authority ls'). Request a validity within the maximum, or leave " +
				"it out to use the CA's default.",
		},
		{
			name: "invalid period with the CA default",
			body: `{"code":"cert_invalid_period"}`,
			want: "The CA's default validity exceeds its own maximum (Default and Max Valid Days in " +
				"'alpacon authority ls'). Request a validity within the maximum, or ask the CA's owner to " +
				"correct its settings.",
		},
		{
			name:      "another coded 400 falls back",
			body:      `{"code":"cert_request_in_progress"}`,
			validDays: 90,
		},
		{
			name:      "transport error falls back",
			transport: errors.New("some network failure"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.transport
			if err == nil {
				err = createError(t, tt.body)
			}
			want := tt.want
			if want == "" {
				want = "Failed to send sign request to server: " + err.Error() + "."
			}
			assert.Equal(t, want, createSignRequestErrorText(err, tt.validDays))
		})
	}
}
