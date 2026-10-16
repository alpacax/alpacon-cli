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
		name         string
		validDays    int
		validDaysSet bool
		want         string
	}{
		{
			name:      "omitted flag leaves valid_days to the CA",
			validDays: 0,
			want:      `{"domain_list":["a.com","b.com"],"ip_list":[]}`,
		},
		{
			name:         "explicit flag is sent",
			validDays:    90,
			validDaysSet: true,
			want:         `{"domain_list":["a.com","b.com"],"ip_list":[],"valid_days":90}`,
		},
		{
			name:      "flag value without the flag set is ignored",
			validDays: 365,
			want:      `{"domain_list":["a.com","b.com"],"ip_list":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := signRequestBody(t, newSignRequest("a.com, b.com", "", tt.validDays, tt.validDaysSet))
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
				require.Error(t, err)
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
		name        string
		body        string
		transport   error
		validDays   int
		wantMatch   string
		wantNoMatch string
	}{
		{
			name:        "invalid period with explicit valid days",
			body:        `{"code":"cert_invalid_period"}`,
			validDays:   900,
			wantMatch:   "The CA refused a validity of 900 days",
			wantNoMatch: "Failed to send sign request",
		},
		{
			name:        "invalid period with the CA default",
			body:        `{"code":"cert_invalid_period"}`,
			wantMatch:   "The CA's default validity exceeds its own maximum",
			wantNoMatch: "Failed to send sign request",
		},
		{
			name:        "another coded 400 falls back",
			body:        `{"code":"cert_request_in_progress"}`,
			validDays:   90,
			wantMatch:   "Failed to send sign request",
			wantNoMatch: "Max Valid Days",
		},
		{
			name:      "transport error falls back",
			transport: errors.New("some network failure"),
			wantMatch: "Failed to send sign request to server: some network failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.transport
			if err == nil {
				err = createError(t, tt.body)
			}
			got := createSignRequestErrorText(err, tt.validDays)
			assert.Contains(t, got, tt.wantMatch)
			if tt.wantNoMatch != "" {
				assert.NotContains(t, got, tt.wantNoMatch)
			}
		})
	}
}
