package cert

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
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

func TestGetCSRList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []CSRResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, CSRResponse{
					ID:         fmt.Sprintf("csr-%d", i),
					CommonName: fmt.Sprintf("cn-%d", i),
					Authority: AuthorityResponse{
						Name: "auth-test",
					},
					RequestedBy: types.UserSummary{Name: "admin"},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, CSRResponse{
					ID:         fmt.Sprintf("csr-p2-%d", i),
					CommonName: fmt.Sprintf("cn-p2-%d", i),
					Authority: AuthorityResponse{
						Name: "auth-test",
					},
					RequestedBy: types.UserSummary{Name: "admin"},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[CSRResponse]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	csrs, err := GetCSRList(ac, "pending")
	if err != nil {
		t.Fatalf("GetCSRList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(csrs) != 150 {
		t.Errorf("expected 150 CSRs, got %d", len(csrs))
	}
}

func TestGetAuthorityList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []AuthorityResponse
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, AuthorityResponse{
					ID:   fmt.Sprintf("auth-%d", i),
					Name: fmt.Sprintf("authority-%d", i),
					Owner: types.UserSummary{
						Name: "admin",
					},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, AuthorityResponse{
					ID:   fmt.Sprintf("auth-p2-%d", i),
					Name: fmt.Sprintf("authority-p2-%d", i),
					Owner: types.UserSummary{
						Name: "admin",
					},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[AuthorityResponse]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	authorities, err := GetAuthorityList(ac)
	if err != nil {
		t.Fatalf("GetAuthorityList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(authorities) != 150 {
		t.Errorf("expected 150 authorities, got %d", len(authorities))
	}
}

func TestGetCertificateList_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []Certificate
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, Certificate{
					ID: fmt.Sprintf("cert-%d", i),
					Authority: AuthoritySummary{
						Name: "auth-test",
					},
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, Certificate{
					ID: fmt.Sprintf("cert-p2-%d", i),
					Authority: AuthoritySummary{
						Name: "auth-test",
					},
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[Certificate]{
			Count:   150,
			Next:    next,
			Results: results,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	certs, err := GetCertificateList(ac)
	if err != nil {
		t.Fatalf("GetCertificateList error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(certs) != 150 {
		t.Errorf("expected 150 certificates, got %d", len(certs))
	}
}

func TestCreateSignRequest(t *testing.T) {
	expectedResponse := SignRequestResponse{
		ID:         "new-csr-id",
		CommonName: "example.com",
		Status:     "requested",
		SubmitURL:  "/api/cert/sign-requests/new-csr-id/submit/",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(expectedResponse)
	}))
	defer server.Close()

	ac := newTestClient(server)
	signReq := SignRequest{
		DomainList: []string{"example.com"},
		ValidDays:  365,
	}

	resp, err := CreateSignRequest(ac, signReq)
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.ID, resp.ID)
	assert.Equal(t, expectedResponse.CommonName, resp.CommonName)
	assert.Equal(t, expectedResponse.SubmitURL, resp.SubmitURL)
}

func TestSubmitCSR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)

		var body CSRSubmit
		err := json.NewDecoder(r.Body).Decode(&body)
		assert.NoError(t, err)
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
		_, _ = w.Write([]byte(`{"status":"signing"}`))
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
		_, _ = w.Write([]byte(`{"status":"denied"}`))
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
				ID:         "test-csr-id",
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
				ID:         "test-csr-id",
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
				ID:         "test-csr-id",
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
				ID:         "test-csr-id",
				CommonName: "example.com",
				Status:     "signing",
				CrtText:    "",
			},
			statusCode:  http.StatusOK,
			expectError: true,
			errorMsg:    "certificate not yet issued for this CSR (status: signing)",
		},
		{
			name: "non-signed CSR with certificate text",
			response: SignRequestDetail{
				ID:         "test-csr-id",
				CommonName: "example.com",
				Status:     "requested",
				CrtText:    "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
			statusCode:  http.StatusOK,
			expectError: true,
			errorMsg:    "certificate not yet issued for this CSR (status: requested)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_ = json.NewEncoder(w).Encode(tt.response)
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
		_, _ = w.Write([]byte(`{"detail":"Not found."}`))
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
		_, _ = w.Write([]byte(`{invalid json`))
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
				ID:      "test-cert-id",
				CrtText: "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
			expectError: false,
		},
		{
			name: "empty certificate text",
			response: Certificate{
				ID:      "test-cert-id",
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
				_ = json.NewEncoder(w).Encode(tt.response)
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
