package ftp

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func createTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestPollTransferStatus(t *testing.T) {
	tests := []struct {
		name        string
		response    TransferStatusResponse
		wantSuccess bool
		wantMessage string
		wantErr     bool
	}{
		{
			name:        "immediate success",
			response:    TransferStatusResponse{Success: boolPtr(true), Message: "completed"},
			wantSuccess: true,
			wantMessage: "completed",
		},
		{
			name:        "immediate failure",
			response:    TransferStatusResponse{Success: boolPtr(false), Message: "permission denied"},
			wantSuccess: false,
			wantMessage: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tt.response)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{
				HTTPClient: ts.Client(),
				BaseURL:    ts.URL,
			}

			success, message, err := PollTransferStatus(ac, "upload", "test-id", 30*time.Second)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSuccess, success)
			assert.Equal(t, tt.wantMessage, message)
		})
	}
}

func TestUploadToS3(t *testing.T) {
	var receivedBody []byte
	var receivedMethod string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	content := []byte("test file content")
	err := uploadToS3(ts.Client(), ts.URL, bytes.NewReader(content))

	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, receivedMethod)
	assert.Equal(t, content, receivedBody)
}

func TestUploadToS3_Failure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	err := uploadToS3(ts.Client(), ts.URL, bytes.NewReader([]byte("data")))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestExecuteBulkUpload(t *testing.T) {
	var bulkReq BulkUploadRequest
	var triggerReq BulkUploadTriggerRequest
	var s3Uploads atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			_ = json.NewDecoder(r.Body).Decode(&bulkReq)
			// Return upload responses with presigned URLs pointing back to this server
			responses := []UploadResponse{
				{ID: "id-1", Name: "file1.txt", UploadURL: "http://" + r.Host + "/s3/file1"},
				{ID: "id-2", Name: "file2.txt", UploadURL: "http://" + r.Host + "/s3/file2"},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responses)

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/s3/"):
			s3Uploads.Add(1)
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk-upload/":
			_ = json.NewDecoder(r.Body).Decode(&triggerReq)
			w.WriteHeader(http.StatusCreated)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{
				Success: boolPtr(true),
				Message: "completed",
			})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &BulkUploadRequest{
		Names:          []string{"file1.txt", "file2.txt"},
		Path:           "/remote/path",
		Server:         "server-id",
		Username:       "admin",
		Groupname:      "developers",
		AllowOverwrite: true,
	}
	contents := [][]byte{[]byte("content1"), []byte("content2")}

	err := executeBulkUpload(ac, request, contents)
	require.NoError(t, err)

	assert.Equal(t, []string{"file1.txt", "file2.txt"}, bulkReq.Names)
	assert.Equal(t, "/remote/path", bulkReq.Path)
	assert.Equal(t, "server-id", bulkReq.Server)
	assert.Equal(t, "admin", bulkReq.Username)
	assert.Equal(t, "developers", bulkReq.Groupname)
	assert.True(t, bulkReq.AllowOverwrite)
	assert.False(t, bulkReq.AllowUnzip)

	assert.Equal(t, int32(2), s3Uploads.Load())
	assert.Equal(t, []string{"id-1", "id-2"}, triggerReq.IDs)
}

func TestExecuteBulkUpload_NoOverwrite(t *testing.T) {
	var bulkReq BulkUploadRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			_ = json.NewDecoder(r.Body).Decode(&bulkReq)
			responses := []UploadResponse{
				{ID: "id-1", Name: "file1.txt", UploadURL: "http://" + r.Host + "/s3/file1"},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responses)

		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk-upload/":
			w.WriteHeader(http.StatusCreated)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{
				Success: boolPtr(true),
			})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &BulkUploadRequest{
		Names:          []string{"file1.txt"},
		Path:           "/remote/path",
		Server:         "server-id",
		AllowOverwrite: false,
	}

	err := executeBulkUpload(ac, request, [][]byte{[]byte("content")})
	require.NoError(t, err)
	assert.False(t, bulkReq.AllowOverwrite)
}

func TestExecuteBulkUpload_WithUnzip(t *testing.T) {
	var bulkReq BulkUploadRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			_ = json.NewDecoder(r.Body).Decode(&bulkReq)
			responses := []UploadResponse{
				{ID: "id-1", Name: "folder.zip", UploadURL: "http://" + r.Host + "/s3/folder"},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responses)

		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk-upload/":
			w.WriteHeader(http.StatusCreated)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: boolPtr(true)})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &BulkUploadRequest{
		Names:          []string{"folder.zip"},
		Path:           "/remote/path",
		Server:         "server-id",
		AllowOverwrite: true,
		AllowUnzip:     true,
	}

	err := executeBulkUpload(ac, request, [][]byte{[]byte("zipdata")})
	require.NoError(t, err)
	assert.True(t, bulkReq.AllowUnzip)
	assert.True(t, bulkReq.AllowOverwrite)
}

func TestDownloadBulk(t *testing.T) {
	var bulkReq BulkDownloadRequest

	zipContent := createTestZip(t, map[string]string{
		"file1.txt": "hello",
		"file2.txt": "world",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/downloads/bulk/":
			_ = json.NewDecoder(r.Body).Decode(&bulkReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(BulkDownloadResponse{
				ID:          "dl-1",
				Name:        "archive.zip",
				Command:     "cmd-1",
				DownloadURL: "http://" + r.Host + "/download/archive.zip",
			})

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/commands/"):
			// Mock event.PollCommandExecution response
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":     "cmd-1",
				"status": "completed",
				"result": "done",
			})

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/download/"):
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipContent)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{
				Success: boolPtr(true),
				Message: "completed",
			})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	dest := t.TempDir()

	err := downloadBulk(ac, []string{"/path/file1.txt", "/path/file2.txt"}, dest, "server-id", "admin", "developers")
	require.NoError(t, err)

	// Verify request body
	assert.Equal(t, []string{"/path/file1.txt", "/path/file2.txt"}, bulkReq.Path)
	assert.Equal(t, "server-id", bulkReq.Server)
	assert.Equal(t, "admin", bulkReq.Username)
	assert.Equal(t, "developers", bulkReq.Groupname)

	// Verify extracted files exist
	content1, err := os.ReadFile(filepath.Join(dest, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content1))

	content2, err := os.ReadFile(filepath.Join(dest, "file2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(content2))

	// Verify temp zip was cleaned up
	_, err = os.Stat(filepath.Join(dest, "archive.zip"))
	assert.True(t, os.IsNotExist(err))
}

func TestExecuteSingleUpload(t *testing.T) {
	var uploadReq UploadRequest
	var s3Uploaded bool
	var triggerCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/":
			_ = json.NewDecoder(r.Body).Decode(&uploadReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(UploadResponse{
				ID:        "single-id-1",
				Name:      "file1.txt",
				UploadURL: "http://" + r.Host + "/s3/file1",
			})

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/s3/"):
			s3Uploaded = true
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{
				Success: boolPtr(true),
				Message: "completed",
			})

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/upload"):
			triggerCalled = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &UploadRequest{
		Name:           "file1.txt",
		Path:           "/remote/path",
		Server:         "server-id",
		Username:       "admin",
		Groupname:      "developers",
		AllowOverwrite: true,
	}

	err := executeSingleUpload(ac, request, []byte("content1"))
	require.NoError(t, err)

	assert.Equal(t, "file1.txt", uploadReq.Name)
	assert.Equal(t, "/remote/path", uploadReq.Path)
	assert.Equal(t, "server-id", uploadReq.Server)
	assert.Equal(t, "admin", uploadReq.Username)
	assert.Equal(t, "developers", uploadReq.Groupname)
	assert.True(t, uploadReq.AllowOverwrite)
	assert.False(t, uploadReq.AllowUnzip)
	assert.True(t, s3Uploaded)
	assert.True(t, triggerCalled)
}

func TestExecuteSingleUpload_TransferFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(UploadResponse{
				ID:        "single-id-1",
				Name:      "file1.txt",
				UploadURL: "http://" + r.Host + "/s3/file1",
			})

		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{
				Success: boolPtr(false),
				Message: "permission denied",
			})

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/upload"):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &UploadRequest{
		Name:   "file1.txt",
		Path:   "/remote/path",
		Server: "server-id",
	}

	err := executeSingleUpload(ac, request, []byte("content"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestExecuteSingleUpload_WithUnzip(t *testing.T) {
	var uploadReq UploadRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/":
			_ = json.NewDecoder(r.Body).Decode(&uploadReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(UploadResponse{
				ID:        "single-id-1",
				Name:      "folder.zip",
				UploadURL: "http://" + r.Host + "/s3/folder",
			})

		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: boolPtr(true)})

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/upload"):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &UploadRequest{
		Name:           "folder.zip",
		Path:           "/remote/path",
		Server:         "server-id",
		AllowOverwrite: true,
		AllowUnzip:     true,
	}

	err := executeSingleUpload(ac, request, []byte("zipdata"))
	require.NoError(t, err)
	assert.True(t, uploadReq.AllowUnzip)
	assert.True(t, uploadReq.AllowOverwrite)
}

func TestExecuteBulkUpload_TransferFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode([]UploadResponse{
				{ID: "id-1", Name: "file1.txt", UploadURL: "http://" + r.Host + "/s3/file1"},
			})

		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk-upload/":
			w.WriteHeader(http.StatusCreated)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{
				Success: boolPtr(false),
				Message: "disk full",
			})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &BulkUploadRequest{
		Names:  []string{"file1.txt"},
		Path:   "/remote/path",
		Server: "server-id",
	}

	err := executeBulkUpload(ac, request, [][]byte{[]byte("content")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestExecuteBulkUpload_MismatchedResponseCount(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			// Return 1 response for 2 files
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode([]UploadResponse{
				{ID: "id-1", Name: "file1.txt", UploadURL: "http://" + r.Host + "/s3/file1"},
			})
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	request := &BulkUploadRequest{
		Names:  []string{"file1.txt", "file2.txt"},
		Path:   "/remote/path",
		Server: "server-id",
	}

	err := executeBulkUpload(ac, request, [][]byte{[]byte("content1"), []byte("content2")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "1 upload slots but 2 files")
}

func TestPollTransferStatus_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Always return nil success (pending state)
		_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: nil, Message: "pending"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	// Use a very short timeout to make the test fast
	success, _, err := PollTransferStatus(ac, "upload", "test-id", 3*time.Second)
	assert.Error(t, err)
	assert.False(t, success)
	assert.Contains(t, err.Error(), "timed out")
}

func TestFetchFromURL_ClientErrorNoRetry(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	_, err := fetchFromURL(ts.Client(), ts.URL, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client error: 403")
	// Should fail on first attempt, not retry
	assert.Equal(t, int32(1), requestCount.Load())
}
