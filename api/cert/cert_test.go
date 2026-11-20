package cert

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(server *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: server.Client(),
		BaseURL:    server.URL,
		Token:      "test-token",
	}
}

func TestGetCSRList_Pagination(t *testing.T) {
	t.Parallel()
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
			for i := range 100 {
				results = append(results, CSRResponse{
					ID:         fmt.Sprintf("csr-%d", i),
					CommonName: fmt.Sprintf("cn-%d", i),
					Authority: AuthoritySummary{
						Name: "auth-test",
					},
					RequestedBy: types.UserSummary{Name: "admin"},
				})
			}
		case "2":
			for i := range 50 {
				results = append(results, CSRResponse{
					ID:         fmt.Sprintf("csr-p2-%d", i),
					CommonName: fmt.Sprintf("cn-p2-%d", i),
					Authority: AuthoritySummary{
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

func TestGetAuthorityIDByName(t *testing.T) {
	t.Parallel()
	authorities := []AuthorityResponse{
		{ID: "aaaa-0000-0000-0000-000000000001", Name: "Root CA"},
		{ID: "aaaa-0000-0000-0000-000000000002", Name: "Intermediate CA"},
	}

	newServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := api.ListResponse[AuthorityResponse]{Count: len(authorities), Results: authorities}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
	}

	t.Run("found by name", func(t *testing.T) {
		ts := newServer()
		defer ts.Close()
		id, err := GetAuthorityIDByName(newTestClient(ts), "Root CA")
		require.NoError(t, err)
		assert.Equal(t, "aaaa-0000-0000-0000-000000000001", id)
	})

	t.Run("not found returns error", func(t *testing.T) {
		ts := newServer()
		defer ts.Close()
		_, err := GetAuthorityIDByName(newTestClient(ts), "Nonexistent CA")
		require.ErrorContains(t, err, "no authority found with name")
	})

	t.Run("uuid fast-path skips fetch", func(t *testing.T) {
		var called bool
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()
		uuid := "550e8400-e29b-41d4-a716-446655440000"
		id, err := GetAuthorityIDByName(newTestClient(ts), uuid)
		require.NoError(t, err)
		assert.Equal(t, uuid, id)
		assert.False(t, called, "should not call server for UUID input")
	})
}

func TestGetAuthorityList_Pagination(t *testing.T) {
	t.Parallel()
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
			for i := range 100 {
				results = append(results, AuthorityResponse{
					ID:   fmt.Sprintf("auth-%d", i),
					Name: fmt.Sprintf("authority-%d", i),
				})
			}
		case "2":
			for i := range 50 {
				results = append(results, AuthorityResponse{
					ID:   fmt.Sprintf("auth-p2-%d", i),
					Name: fmt.Sprintf("authority-p2-%d", i),
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
	t.Parallel()
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
			for i := range 100 {
				results = append(results, Certificate{
					ID: fmt.Sprintf("cert-%d", i),
					Authority: AuthoritySummary{
						Name: "auth-test",
					},
				})
			}
		case "2":
			for i := range 50 {
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
	t.Parallel()
	expectedResponse := SignRequestResponse{
		ID:         "new-csr-id",
		CommonName: "example.com",
		Status:     "requested",
		SubmitURL:  "/api/cert/sign-requests/new-csr-id/submit/",
	}

	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
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
	require.NoError(t, err)
	assert.Equal(t, expectedResponse.ID, resp.ID)
	assert.Equal(t, expectedResponse.CommonName, resp.CommonName)
	assert.Equal(t, expectedResponse.SubmitURL, resp.SubmitURL)

	// omitempty: zero-value fields must not be sent to avoid api_insufficient_data errors
	assert.NotContains(t, capturedBody, "csr_text", "csr_text must be omitted when empty")
	assert.NotContains(t, capturedBody, "requested_by", "requested_by must be omitted when empty")
}

func TestSubmitCSR(t *testing.T) {
	t.Parallel()
	csrData := []byte("-----BEGIN CERTIFICATE REQUEST-----\nMIIB...\n-----END CERTIFICATE REQUEST-----")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/cert/sign-requests/test-id/submit/", r.URL.Path)

		var body CSRSubmit
		err := json.NewDecoder(r.Body).Decode(&body)
		assert.NoError(t, err)
		assert.Equal(t, string(csrData), body.CsrText)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ac := newTestClient(server)

	err := SubmitCSR(ac, csrData, SignRequestResponse{SubmitURL: "/api/cert/sign-requests/test-id/submit/"})
	assert.NoError(t, err)
}

func TestApproveCSR(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	assert.NotNil(t, body)
}

func TestDenyCSR(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	assert.NotNil(t, body)
}

func TestDeleteCSR(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ac := newTestClient(server)
	err := DeleteCSR(ac, "test-csr-id")
	assert.NoError(t, err)
}

func TestDeleteCA_RefusedWithHistory(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"cert_authority_cannot_be_deleted"}`))
	}))
	defer server.Close()

	ac := newTestClient(server)
	err := DeleteCA(ac, "test-ca-id")
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, CodeAuthorityCannotBeDeleted, code)
}

func TestDownloadCertificateByCSR(t *testing.T) {
	t.Parallel()
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
				require.ErrorContains(t, err, tt.errorMsg)
				assert.NoFileExists(t, filePath)
			} else {
				require.NoError(t, err)
				content, readErr := os.ReadFile(filePath)
				require.NoError(t, readErr)
				assert.Equal(t, tt.response.CrtText, string(content))
			}
		})
	}
}

func TestDownloadCertificateByCSR_APIError(t *testing.T) {
	t.Parallel()
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
	require.Error(t, err)
	assert.NoFileExists(t, filePath)
}

func TestDownloadCertificateByCSR_InvalidJSON(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
				require.NoError(t, err)
				content, readErr := os.ReadFile(filePath)
				require.NoError(t, readErr)
				assert.Equal(t, tt.response.CrtText, string(content))
			}
		})
	}
}

func TestCertCallsRefuseAPathTraversingIDBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(*client.AlpaconClient, string) error
	}{
		{name: "GetCSRDetail", call: func(ac *client.AlpaconClient, id string) error { _, err := GetCSRDetail(ac, id); return err }},
		{name: "GetCertificateDetail", call: func(ac *client.AlpaconClient, id string) error { _, err := GetCertificateDetail(ac, id); return err }},
		{name: "ApproveCSR", call: func(ac *client.AlpaconClient, id string) error { _, err := ApproveCSR(ac, id); return err }},
		{name: "DenyCSR", call: func(ac *client.AlpaconClient, id string) error { _, err := DenyCSR(ac, id); return err }},
		{name: "RetryCSR", call: func(ac *client.AlpaconClient, id string) error { _, err := RetryCSR(ac, id); return err }},
		{name: "DeleteCSR", call: DeleteCSR},
		{name: "GetAuthorityDetail", call: func(ac *client.AlpaconClient, id string) error { _, err := GetAuthorityDetail(ac, id); return err }},
		{name: "DeleteCA", call: DeleteCA},
		{name: "UpdateAuthority", call: func(ac *client.AlpaconClient, id string) error { _, err := UpdateAuthority(ac, id); return err }},
		{name: "DownloadCRL", call: func(ac *client.AlpaconClient, id string) error { return DownloadCRL(ac, id, "") }},
		{name: "DownloadRootCertificate", call: func(ac *client.AlpaconClient, id string) error { return DownloadRootCertificate(ac, id, "") }},
		{name: "GetRevokeRequestDetail", call: func(ac *client.AlpaconClient, id string) error { _, err := GetRevokeRequestDetail(ac, id); return err }},
		{name: "ApproveRevokeRequest", call: func(ac *client.AlpaconClient, id string) error { _, err := ApproveRevokeRequest(ac, id); return err }},
		{name: "DenyRevokeRequest", call: func(ac *client.AlpaconClient, id string) error { _, err := DenyRevokeRequest(ac, id); return err }},
		{name: "RetryRevokeRequest", call: func(ac *client.AlpaconClient, id string) error { _, err := RetryRevokeRequest(ac, id); return err }},
		{name: "CancelRevokeRequest", call: CancelRevokeRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)

			err := tt.call(newTestClient(ts), "../../etc/test")

			require.ErrorIs(t, err, utils.ErrInvalidID)
		})
	}
}

// UpdateAuthority opens an editor, and ApproveCSR, DenyCSR, DeleteCSR and DeleteCA
// already have happy-path tests, so none of them is driven here.
func TestCertCallsTrimAValidIDIntoOnePathSegment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tests := []struct {
		name string
		call func(*client.AlpaconClient, string) error
		want string
	}{
		{"GetCSRDetail", func(ac *client.AlpaconClient, id string) error { _, err := GetCSRDetail(ac, id); return err }, "GET /api/cert/sign-requests/42/"},
		{"GetCertificateDetail", func(ac *client.AlpaconClient, id string) error { _, err := GetCertificateDetail(ac, id); return err }, "GET /api/cert/certificates/42/"},
		{"RetryCSR", func(ac *client.AlpaconClient, id string) error { _, err := RetryCSR(ac, id); return err }, "POST /api/cert/sign-requests/42/retry/"},
		{"GetAuthorityDetail", func(ac *client.AlpaconClient, id string) error { _, err := GetAuthorityDetail(ac, id); return err }, "GET /api/cert/authorities/42/"},
		{"DownloadCRL", func(ac *client.AlpaconClient, id string) error {
			return DownloadCRL(ac, id, filepath.Join(dir, "crl.pem"))
		}, "GET /api/cert/authorities/42/crl/"},
		{"DownloadRootCertificate", func(ac *client.AlpaconClient, id string) error {
			return DownloadRootCertificate(ac, id, filepath.Join(dir, "root.pem"))
		}, "GET /api/cert/authorities/42/"},
		{"GetRevokeRequestDetail", func(ac *client.AlpaconClient, id string) error { _, err := GetRevokeRequestDetail(ac, id); return err }, "GET /api/cert/revoke-requests/42/"},
		{"ApproveRevokeRequest", func(ac *client.AlpaconClient, id string) error { _, err := ApproveRevokeRequest(ac, id); return err }, "POST /api/cert/revoke-requests/42/approve/"},
		{"DenyRevokeRequest", func(ac *client.AlpaconClient, id string) error { _, err := DenyRevokeRequest(ac, id); return err }, "POST /api/cert/revoke-requests/42/deny/"},
		{"RetryRevokeRequest", func(ac *client.AlpaconClient, id string) error { _, err := RetryRevokeRequest(ac, id); return err }, "POST /api/cert/revoke-requests/42/retry/"},
		{"CancelRevokeRequest", CancelRevokeRequest, "DELETE /api/cert/revoke-requests/42/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec atomic.Value
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.Store(r.Method + " " + r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(ts.Close)

			err := tt.call(newTestClient(ts), " 42 ")

			require.NoError(t, err)
			assert.Equal(t, tt.want, rec.Load())
		})
	}
}

func TestGetAuthorityIDByName_ReturnsOnlyAOneSegmentID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, id, want string }{
		{name: "padded_id", id: " id-1 ", want: "id-1"},
		{name: "parent_traversal", id: "../x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[AuthorityResponse]{Count: 1, Results: []AuthorityResponse{{ID: tc.id, Name: "my-ca"}}})
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			id, err := GetAuthorityIDByName(ac, "my-ca")

			if tc.want == "" {
				require.ErrorIs(t, err, api.ErrUnusableID)
				assert.Empty(t, id)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, id)
		})
	}
}

// The list route names the approver and the denier by user ID; only the detail route expands them.
func TestGetRevokeRequestList_ListsDecidedRequests(t *testing.T) {
	t.Parallel()

	const listPage = `
	{
	  "count": 3,
	  "next": null,
	  "results": [
	    {
	      "id": "req-approved",
	      "certificate": "cert-1",
	      "authority": {
	        "id": "ca-1",
	        "name": "my-ca"
	      },
	      "common_name": "a.example.com",
	      "serial_number": "01",
	      "reason": 0,
	      "status": "revoked",
	      "requested_reason": "",
	      "requested_by": {
	        "id": "user-1",
	        "username": "alice",
	        "name": "Alice"
	      },
	      "approved_by": "550e8400-e29b-41d4-a716-446655440000",
	      "denied_by": null,
	      "added_at": "2026-01-02T03:04:05Z",
	      "error_message": "",
	      "handled_at": "2026-01-02T03:05:00Z"
	    },
	    {
	      "id": "req-denied",
	      "certificate": "cert-2",
	      "authority": {
	        "id": "ca-1",
	        "name": "my-ca"
	      },
	      "common_name": "b.example.com",
	      "serial_number": "02",
	      "reason": 0,
	      "status": "denied",
	      "requested_reason": "",
	      "requested_by": {
	        "id": "user-1",
	        "username": "alice",
	        "name": "Alice"
	      },
	      "approved_by": null,
	      "denied_by": "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47",
	      "added_at": "2026-01-02T03:04:05Z",
	      "error_message": "",
	      "handled_at": "2026-01-02T03:05:00Z"
	    },
	    {
	      "id": "req-open",
	      "certificate": "cert-3",
	      "authority": {
	        "id": "ca-1",
	        "name": "my-ca"
	      },
	      "common_name": "c.example.com",
	      "serial_number": "03",
	      "reason": 0,
	      "status": "requested",
	      "requested_reason": "",
	      "requested_by": {
	        "id": "user-1",
	        "username": "alice",
	        "name": "Alice"
	      },
	      "approved_by": null,
	      "denied_by": null,
	      "added_at": "2026-01-02T03:04:05Z",
	      "error_message": "",
	      "handled_at": null
	    }
	  ]
	}
	`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listPage))
	}))
	defer ts.Close()

	requests, err := GetRevokeRequestList(newTestClient(ts), "", "")

	require.NoError(t, err)
	type row struct {
		ID          string
		Status      string
		Authority   string
		RequestedBy string
	}
	var got []row
	for _, r := range requests {
		got = append(got, row{r.ID, r.Status, r.Authority, r.RequestedBy})
	}
	assert.Equal(t, []row{
		{"req-approved", "revoked", "my-ca", "Alice"},
		{"req-denied", "denied", "my-ca", "Alice"},
		{"req-open", "requested", "my-ca", "Alice"},
	}, got)
}

func TestGetAuthorityListOutputsRowsWithoutOwner(t *testing.T) {
	oldFormat := utils.OutputFormat
	t.Cleanup(func() { utils.OutputFormat = oldFormat })
	tests := []struct {
		name        string
		ownerSuffix string
	}{
		{name: "owner omitted"},
		{name: "legacy owner ignored", ownerSuffix: `,"owner":{"name":"legacy-user"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"ca-1","name":"Root CA","organization":"Example","domain":"example.com","root_valid_days":3650,"default_valid_days":365,"max_valid_days":730,"agent":{"name":"my-server"},"signed_at":"2026-01-01T00:00:00Z"` + tt.ownerSuffix + `}]}`))
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			rows, err := GetAuthorityList(ac)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
				t.Run(format, func(t *testing.T) {
					utils.OutputFormat = format
					output := testutil.CaptureStdout(t, func() { assert.NoError(t, utils.PrintTable(rows)) })
					assert.NotContains(t, strings.ToLower(output), "owner")
					assert.NotContains(t, output, "legacy-user")
					assert.Contains(t, output, "my-server")
					if format == utils.OutputFormatJSON {
						var decoded []map[string]any
						require.NoError(t, json.Unmarshal([]byte(output), &decoded))
						require.Len(t, decoded, 1)
						assert.Len(t, decoded[0], 9)
						assert.Equal(t, "ca-1", decoded[0]["id"])
						assert.Equal(t, "Root CA", decoded[0]["name"])
						assert.Equal(t, "Example", decoded[0]["organization"])
						assert.Equal(t, "example.com", decoded[0]["domain"])
						assert.InDelta(t, 3650, decoded[0]["root_valid_days"], 0)
						assert.InDelta(t, 365, decoded[0]["default_valid_days"], 0)
						assert.InDelta(t, 730, decoded[0]["max_valid_days"], 0)
						assert.Equal(t, "my-server", decoded[0]["server"])
						assert.NotEmpty(t, decoded[0]["signed_at"])
					}
				})
			}
		})
	}
}

func TestGetCertificateList_JSONKeepsRevocationState(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"results":[
			{"id":"550e8400-e29b-41d4-a716-446655440000","common_name":"my-server","serial_number":"0a1b","status":"revoked","is_revoked":true,"revoked_at":"2026-11-07T18:30:45+09:00","signed_at":"2026-11-01T00:00:00Z","expires_at":"2027-11-01T00:00:00Z"},
			{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","common_name":"my-server","serial_number":"0a1c","status":"signed","is_revoked":false,"revoked_at":null,"signed_at":"2026-11-01T00:00:00Z","expires_at":"2027-11-01T00:00:00Z"}
		]}`))
	}))
	defer ts.Close()

	certs, err := GetCertificateList(newTestClient(ts))

	require.NoError(t, err)
	body, err := json.Marshal(certs)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"id":"550e8400-e29b-41d4-a716-446655440000","authority":"","common_name":"my-server","serial_number":"0a1b","status":"revoked","is_revoked":true,"revoked_at":"2026-11-07T09:30:45Z","csr":"","valid_days":0,"signed_at":"2026-11-01T00:00:00Z","expires_at":"2027-11-01T00:00:00Z","signed_by":"","renewed_by":""},
		{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","authority":"","common_name":"my-server","serial_number":"0a1c","status":"signed","is_revoked":false,"revoked_at":null,"csr":"","valid_days":0,"signed_at":"2026-11-01T00:00:00Z","expires_at":"2027-11-01T00:00:00Z","signed_by":"","renewed_by":""}
	]`, string(body))
}

func TestGetRevokeRequestList_JSONKeepsFailureMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"results":[
			{"id":"550e8400-e29b-41d4-a716-446655440000","status":"failed","error_message":"authority unreachable","handled_at":"2026-11-07T18:30:45+09:00","added_at":"2026-11-07T09:00:00Z"},
			{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","status":"requested","error_message":"","handled_at":null,"added_at":"2026-11-07T09:00:00Z"}
		]}`))
	}))
	defer ts.Close()

	requests, err := GetRevokeRequestList(newTestClient(ts), "", "")

	require.NoError(t, err)
	body, err := json.Marshal(requests)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"id":"550e8400-e29b-41d4-a716-446655440000","common_name":"","authority":"","serial_number":"","status":"failed","requested_by":"","added_at":"2026-11-07T09:00:00Z","error_message":"authority unreachable","handled_at":"2026-11-07T09:30:45Z"},
		{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","common_name":"","authority":"","serial_number":"","status":"requested","requested_by":"","added_at":"2026-11-07T09:00:00Z","error_message":"","handled_at":null}
	]`, string(body))
}
