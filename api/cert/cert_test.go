package cert

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func newTestClient(server *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: server.Client(),
		BaseURL:    server.URL,
		Token:      "test-token",
	}
}

func TestCreateSignRequest(t *testing.T) {
	expectedResponse := SignRequestResponse{
		Id:         "new-csr-id",
		CommonName: "example.com",
		Status:     "requested",
		SubmitURL:  "/api/cert/sign-requests/new-csr-id/submit/",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(expectedResponse)
	}))
	defer server.Close()

	ac := newTestClient(server)
	signReq := SignRequest{
		DomainList: []string{"example.com"},
		ValidDays:  365,
	}

	resp, err := CreateSignRequest(ac, signReq)
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Id, resp.Id)
	assert.Equal(t, expectedResponse.CommonName, resp.CommonName)
	assert.Equal(t, expectedResponse.SubmitURL, resp.SubmitURL)
}

func TestSubmitCSR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)

		var body CSRSubmit
		json.NewDecoder(r.Body).Decode(&body)
		assert.NotEmpty(t, body.CsrText)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ac := newTestClient(server)
	csrData := []byte("-----BEGIN CERTIFICATE REQUEST-----\nMIIB...\n-----END CERTIFICATE REQUEST-----")

	err := SubmitCSR(ac, csrData, "/api/cert/sign-requests/test-id/submit/")
	assert.NoError(t, err)
}

func TestApproveCSR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "approve")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"signing"}`))
	}))
	defer server.Close()

	ac := newTestClient(server)
	body, err := ApproveCSR(ac, "test-csr-id")
	assert.NoError(t, err)
	assert.NotNil(t, body)
}

func TestDenyCSR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "deny")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"denied"}`))
	}))
	defer server.Close()

	ac := newTestClient(server)
	body, err := DenyCSR(ac, "test-csr-id")
	assert.NoError(t, err)
	assert.NotNil(t, body)
}

func TestDeleteCSR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ac := newTestClient(server)
	err := DeleteCSR(ac, "test-csr-id")
	assert.NoError(t, err)
}

func TestDownloadCertificateByCSR(t *testing.T) {
	tests := []struct {
		name        string
		response    SignRequestDetail
		statusCode  int
		expectError bool
		errorMsg    string
	}{
		{
			name: "signed CSR with certificate",
			response: SignRequestDetail{
				Id:         "test-csr-id",
				CommonName: "example.com",
				Status:     "signed",
				CrtText:    "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
			statusCode:  http.StatusOK,
			expectError: false,
		},
		{
			name: "requested CSR without certificate",
			response: SignRequestDetail{
				Id:         "test-csr-id",
				CommonName: "example.com",
				Status:     "requested",
				CrtText:    "",
			},
			statusCode:  http.StatusOK,
			expectError: true,
			errorMsg:    "certificate not yet issued for this CSR (status: requested)",
		},
		{
			name: "denied CSR",
			response: SignRequestDetail{
				Id:         "test-csr-id",
				CommonName: "example.com",
				Status:     "denied",
				CrtText:    "",
			},
			statusCode:  http.StatusOK,
			expectError: true,
			errorMsg:    "certificate not yet issued for this CSR (status: denied)",
		},
		{
			name: "signing in progress",
			response: SignRequestDetail{
				Id:         "test-csr-id",
				CommonName: "example.com",
				Status:     "signing",
				CrtText:    "",
			},
			statusCode:  http.StatusOK,
			expectError: true,
			errorMsg:    "certificate not yet issued for this CSR (status: signing)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			ac := newTestClient(server)
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "test.crt")

			err := DownloadCertificateByCSR(ac, "test-csr-id", filePath)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.NoFileExists(t, filePath)
			} else {
				assert.NoError(t, err)
				content, readErr := os.ReadFile(filePath)
				assert.NoError(t, readErr)
				assert.Equal(t, tt.response.CrtText, string(content))
			}
		})
	}
}

func TestDownloadCertificateByCSR_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"detail":"Not found."}`))
	}))
	defer server.Close()

	ac := newTestClient(server)
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.crt")

	err := DownloadCertificateByCSR(ac, "nonexistent-id", filePath)
	assert.Error(t, err)
	assert.NoFileExists(t, filePath)
}

func TestDownloadCertificateByCSR_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	ac := newTestClient(server)
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.crt")

	err := DownloadCertificateByCSR(ac, "test-csr-id", filePath)
	assert.Error(t, err)
}

func TestDownloadCertificate(t *testing.T) {
	tests := []struct {
		name        string
		response    Certificate
		expectError bool
	}{
		{
			name: "valid certificate",
			response: Certificate{
				Id:      "test-cert-id",
				CrtText: "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
			expectError: false,
		},
		{
			name: "empty certificate text",
			response: Certificate{
				Id:      "test-cert-id",
				CrtText: "",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			ac := newTestClient(server)
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "test.crt")

			err := DownloadCertificate(ac, "test-cert-id", filePath)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				content, readErr := os.ReadFile(filePath)
				assert.NoError(t, readErr)
				assert.Equal(t, tt.response.CrtText, string(content))
			}
		})
	}
}
