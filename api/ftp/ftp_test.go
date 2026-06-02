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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
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
	err := uploadToS3(ts.Client(), ts.URL, bytes.NewReader(content), int64(len(content)))

	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, receivedMethod)
	assert.Equal(t, content, receivedBody)
}

func TestUploadToS3_Failure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	err := uploadToS3(ts.Client(), ts.URL, bytes.NewReader([]byte("data")), int64(len("data")))
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
	files := []io.Reader{bytes.NewReader([]byte("content1")), bytes.NewReader([]byte("content2"))}
	sizes := []int64{int64(len("content1")), int64(len("content2"))}

	err := executeBulkUpload(ac, request, files, sizes)
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

func TestExecuteBulkUpload_UploadsConcurrently(t *testing.T) {
	uploadsEntered := make(chan struct{}, 2)
	releaseUploads := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUploads) }) }
	defer release()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			responses := []UploadResponse{
				{ID: "id-1", Name: "file1.txt", UploadURL: "http://" + r.Host + "/s3/file1"},
				{ID: "id-2", Name: "file2.txt", UploadURL: "http://" + r.Host + "/s3/file2"},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responses)

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/s3/"):
			uploadsEntered <- struct{}{}
			<-releaseUploads
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
		Names:  []string{"file1.txt", "file2.txt"},
		Path:   "/remote/path",
		Server: "server-id",
	}
	files := []io.Reader{bytes.NewReader([]byte("content1")), bytes.NewReader([]byte("content2"))}
	sizes := []int64{int64(len("content1")), int64(len("content2"))}

	errCh := make(chan error, 1)
	go func() {
		errCh <- executeBulkUpload(ac, request, files, sizes)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-uploadsEntered:
		case <-time.After(2 * time.Second):
			release()
			t.Fatal("timed out waiting for concurrent uploads to enter handler")
		}
	}
	release()
	err := <-errCh
	require.NoError(t, err)
}

func TestExecuteBulkUpload_PollsConcurrently(t *testing.T) {
	pollsEntered := make(chan struct{}, 2)
	releasePolls := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePolls) }) }
	defer release()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
			responses := []UploadResponse{
				{ID: "id-1", Name: "file1.txt"},
				{ID: "id-2", Name: "file2.txt"},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responses)

		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk-upload/":
			w.WriteHeader(http.StatusCreated)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			pollsEntered <- struct{}{}
			<-releasePolls
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: boolPtr(true)})
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
	files := []io.Reader{bytes.NewReader([]byte("content1")), bytes.NewReader([]byte("content2"))}
	sizes := []int64{int64(len("content1")), int64(len("content2"))}

	errCh := make(chan error, 1)
	go func() {
		errCh <- executeBulkUpload(ac, request, files, sizes)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-pollsEntered:
		case <-time.After(2 * time.Second):
			release()
			t.Fatal("timed out waiting for concurrent polls to enter handler")
		}
	}
	release()
	err := <-errCh
	require.NoError(t, err)
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

	files := []io.Reader{bytes.NewReader([]byte("content"))}
	sizes := []int64{int64(len("content"))}
	err := executeBulkUpload(ac, request, files, sizes)
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

	files := []io.Reader{bytes.NewReader([]byte("zipdata"))}
	sizes := []int64{int64(len("zipdata"))}
	err := executeBulkUpload(ac, request, files, sizes)
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

	err := downloadBulk(ac, []string{"/path/file1.txt", "/path/file2.txt"}, dest, "server-id", "admin", "developers", "")
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

func TestDownloadBulk_PreservesExistingArchiveName(t *testing.T) {
	zipContent := createTestZip(t, map[string]string{
		"file.txt": "downloaded",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/downloads/bulk/":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(BulkDownloadResponse{
				ID:          "dl-1",
				Name:        "archive.zip",
				Command:     "cmd-1",
				DownloadURL: "http://" + r.Host + "/download/archive.zip",
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/commands/"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":     "cmd-1",
				"status": "completed",
				"result": "done",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/download/archive.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: boolPtr(true)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	dest := t.TempDir()
	archivePath := filepath.Join(dest, "archive.zip")
	require.NoError(t, os.WriteFile(archivePath, []byte("existing-archive"), 0644))

	err := downloadBulk(ac, []string{"/path/file.txt"}, dest, "server-id", "admin", "developers", "")
	require.NoError(t, err)

	content, readErr := os.ReadFile(archivePath)
	require.NoError(t, readErr)
	assert.Equal(t, "existing-archive", string(content))

	matches, globErr := filepath.Glob(filepath.Join(dest, ".alpacon-download-*.zip"))
	require.NoError(t, globErr)
	assert.Empty(t, matches)
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

	err := executeSingleUpload(ac, request, bytes.NewReader([]byte("content1")), int64(len("content1")))
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

	err := executeSingleUpload(ac, request, bytes.NewReader([]byte("content")), int64(len("content")))
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

	err := executeSingleUpload(ac, request, bytes.NewReader([]byte("zipdata")), int64(len("zipdata")))
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

	files := []io.Reader{bytes.NewReader([]byte("content"))}
	sizes := []int64{int64(len("content"))}
	err := executeBulkUpload(ac, request, files, sizes)
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

	files := []io.Reader{bytes.NewReader([]byte("content1")), bytes.NewReader([]byte("content2"))}
	sizes := []int64{int64(len("content1")), int64(len("content2"))}
	err := executeBulkUpload(ac, request, files, sizes)
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

	dest := filepath.Join(t.TempDir(), "download.bin")
	_, err := fetchFromURLToFile(ts.Client(), ts.URL, dest, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client error: 403")
	// Should fail on first attempt, not retry
	assert.Equal(t, int32(1), requestCount.Load())
	_, statErr := os.Stat(dest)
	assert.True(t, os.IsNotExist(statErr))
}

func TestFetchFromURLToFile_ReadErrorKeepsExistingFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
	}))
	defer ts.Close()

	dest := filepath.Join(t.TempDir(), "download.bin")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0644))

	written, err := fetchFromURLToFile(ts.Client(), ts.URL, dest, 1)
	require.Error(t, err)
	assert.Equal(t, int64(len("partial")), written)

	content, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	assert.Equal(t, "existing", string(content))

	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(dest), ".alpacon-*.tmp"))
	require.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestSaveDownloadedURL_RecursiveUsesTempArchive(t *testing.T) {
	zipContent := createTestZip(t, map[string]string{
		"folder/file.txt": "downloaded",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipContent)
	}))
	defer ts.Close()

	dest := t.TempDir()
	existingArchive := filepath.Join(dest, "folder.zip")
	require.NoError(t, os.WriteFile(existingArchive, []byte("existing-archive"), 0644))

	savedPath, written, err := saveDownloadedURL(ts.Client(), ts.URL, dest, "/remote/folder", true, 1)
	require.NoError(t, err)
	assert.Equal(t, dest, savedPath)
	assert.Equal(t, int64(len(zipContent)), written)

	content, readErr := os.ReadFile(existingArchive)
	require.NoError(t, readErr)
	assert.Equal(t, "existing-archive", string(content))

	extracted, readErr := os.ReadFile(filepath.Join(dest, "folder", "file.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "downloaded", string(extracted))

	matches, globErr := filepath.Glob(filepath.Join(dest, ".alpacon-download-*.zip"))
	require.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestDownloadFile_InputValidation(t *testing.T) {
	tests := []struct {
		name    string
		sources []string
		wantErr string
	}{
		{"empty sources", []string{}, "no source paths provided"},
		{"mixed servers", []string{"server-a:/path/file1.txt", "server-b:/path/file2.txt"}, "all sources must be on the same server"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := DownloadFile(&client.AlpaconClient{}, tt.sources, "/tmp/dest", "", "", false, "")
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestDownloadFile_SpaceInPath(t *testing.T) {
	// Verify that a path with spaces is preserved as a single path,
	// not split by whitespace (the old strings.Fields bug).
	serverResp := api.ListResponse[server.ServerDetails]{
		Count:   1,
		Results: []server.ServerDetails{{ID: "srv-123", Name: "my-server"}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/api/servers/servers"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(serverResp)
		case r.URL.Path == "/api/webftp/downloads/" && r.Method == http.MethodPost:
			// The download API should receive the full path with spaces
			var req DownloadRequest
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)
			assert.Equal(t, "/path/my file.txt", req.Path)
			// Return an error to stop the flow — we only care about path parsing
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	sources := []string{"my-server:/path/my file.txt"}
	// Error expected since the mock doesn't complete the full flow,
	// but the key assertion is that the path was not split.
	_ = DownloadFile(ac, sources, "/tmp/dest", "", "", false, "")
}

func TestDownloadFile_SingleVsBulkRouting(t *testing.T) {
	serverResp := api.ListResponse[server.ServerDetails]{
		Count:   1,
		Results: []server.ServerDetails{{ID: "srv-123", Name: "my-server"}},
	}

	tests := []struct {
		name       string
		sources    []string
		expectBulk bool
	}{
		{
			name:       "single source routes to single download",
			sources:    []string{"my-server:/path/file.txt"},
			expectBulk: false,
		},
		{
			name:       "multiple sources route to bulk download",
			sources:    []string{"my-server:/path/file1.txt", "my-server:/path/file2.txt"},
			expectBulk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hitSingle, hitBulk bool

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/api/servers/servers"):
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(serverResp)
				case r.URL.Path == "/api/webftp/downloads/bulk/" && r.Method == http.MethodPost:
					hitBulk = true
					w.WriteHeader(http.StatusBadRequest)
				case r.URL.Path == "/api/webftp/downloads/" && r.Method == http.MethodPost:
					hitSingle = true
					w.WriteHeader(http.StatusBadRequest)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{
				HTTPClient: ts.Client(),
				BaseURL:    ts.URL,
			}

			// Error expected since mock doesn't complete the flow
			_ = DownloadFile(ac, tt.sources, "/tmp/dest", "", "", false, "")

			if tt.expectBulk {
				assert.True(t, hitBulk, "expected bulk download API to be called")
				assert.False(t, hitSingle, "single download API should not be called")
			} else {
				assert.True(t, hitSingle, "expected single download API to be called")
				assert.False(t, hitBulk, "bulk download API should not be called")
			}
		})
	}
}

// TestExecuteSingleUpload_WorkSession verifies the WorkSession field is sent
// when present and omitted when empty, on the single upload create-request.
func TestExecuteSingleUpload_WorkSession(t *testing.T) {
	tests := []struct {
		name            string
		workSessionID   string
		wantKeyPresent  bool
		wantWorkSession string
	}{
		{"empty work-session is omitted from body", "", false, ""},
		{"work-session ID is sent on POST body", "ws-uuid-123", true, "ws-uuid-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawBody map[string]any

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/":
					body, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(body, &rawBody)
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(UploadResponse{
						ID:        "single-id-1",
						Name:      "file1.txt",
						UploadURL: "http://" + r.Host + "/s3/file1",
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

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			request := &UploadRequest{
				Name:        "file1.txt",
				Path:        "/remote/path",
				Server:      "server-id",
				WorkSession: tt.workSessionID,
			}

			err := executeSingleUpload(ac, request, bytes.NewReader([]byte("content")), int64(len("content")))
			require.NoError(t, err)

			v, present := rawBody["work_session"]
			assert.Equal(t, tt.wantKeyPresent, present)
			if tt.wantKeyPresent {
				assert.Equal(t, tt.wantWorkSession, v)
			}
		})
	}
}

func TestUploadLocalFileAsUsesRemoteBasenameAndDirectory(t *testing.T) {
	var uploadReq UploadRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/servers/servers"):
			_ = json.NewEncoder(w).Encode(api.ListResponse[server.ServerDetails]{
				Count:   1,
				Results: []server.ServerDetails{{ID: "srv-123", Name: "prod"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/":
			_ = json.NewDecoder(r.Body).Decode(&uploadReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(UploadResponse{
				ID:        "upload-1",
				Name:      "app.conf",
				UploadURL: "http://" + r.Host + "/s3/app.conf",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/s3/app.conf":
			body, _ := io.ReadAll(r.Body)
			assert.Equal(t, "edited", string(body))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
			_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: boolPtr(true)})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/upload"):
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	localFile := filepath.Join(t.TempDir(), ".alpacon-edit-random")
	require.NoError(t, os.WriteFile(localFile, []byte("edited"), 0600))

	err := UploadLocalFileAs(ac, localFile, "prod", "/etc/app.conf", "root", "ops", "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "app.conf", uploadReq.Name)
	assert.Equal(t, "/etc/", uploadReq.Path)
	assert.Equal(t, "srv-123", uploadReq.Server)
	assert.Equal(t, "root", uploadReq.Username)
	assert.Equal(t, "ops", uploadReq.Groupname)
	assert.True(t, uploadReq.AllowOverwrite)
	assert.Equal(t, "ws-1", uploadReq.WorkSession)
}

func TestUploadLocalFileAsRejectsInvalidRemoteBasename(t *testing.T) {
	for _, remotePath := range []string{"/tmp/..", "/tmp/app.conf/", `/tmp/..\saved`} {
		t.Run(remotePath, func(t *testing.T) {
			err := UploadLocalFileAs(&client.AlpaconClient{}, "/tmp/local", "prod", remotePath, "", "", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "file name")
		})
	}
}

// TestExecuteBulkUpload_WorkSession verifies the WorkSession field is sent
// when present and omitted when empty, on the bulk upload create-request.
func TestExecuteBulkUpload_WorkSession(t *testing.T) {
	tests := []struct {
		name            string
		workSessionID   string
		wantKeyPresent  bool
		wantWorkSession string
	}{
		{"empty work-session is omitted from body", "", false, ""},
		{"work-session ID is sent on POST body", "ws-uuid-789", true, "ws-uuid-789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawBody map[string]any

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk/":
					body, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(body, &rawBody)
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode([]UploadResponse{
						{ID: "id-1", Name: "file1.txt", UploadURL: "http://" + r.Host + "/s3/file1"},
					})
				case r.Method == http.MethodPut:
					w.WriteHeader(http.StatusOK)
				case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/uploads/bulk-upload/":
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status/"):
					_ = json.NewEncoder(w).Encode(TransferStatusResponse{Success: boolPtr(true)})
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			request := &BulkUploadRequest{
				Names:       []string{"file1.txt"},
				Path:        "/remote/path",
				Server:      "server-id",
				WorkSession: tt.workSessionID,
			}
			files := []io.Reader{bytes.NewReader([]byte("content"))}
			sizes := []int64{int64(len("content"))}

			err := executeBulkUpload(ac, request, files, sizes)
			require.NoError(t, err)

			v, present := rawBody["work_session"]
			assert.Equal(t, tt.wantKeyPresent, present)
			if tt.wantKeyPresent {
				assert.Equal(t, tt.wantWorkSession, v)
			}
		})
	}
}

// TestDownloadFile_WorkSession verifies the WorkSession field is sent on both
// single- and bulk-download create-requests when DownloadFile is invoked with
// a non-empty workSessionID, and omitted when the ID is empty.
func TestDownloadFile_WorkSession(t *testing.T) {
	serverResp := api.ListResponse[server.ServerDetails]{
		Count:   1,
		Results: []server.ServerDetails{{ID: "srv-123", Name: "my-server"}},
	}

	tests := []struct {
		name            string
		sources         []string
		downloadPath    string
		workSessionID   string
		wantKeyPresent  bool
		wantWorkSession string
	}{
		{
			name:           "single download omits work_session when empty",
			sources:        []string{"my-server:/path/file.txt"},
			downloadPath:   "/api/webftp/downloads/",
			workSessionID:  "",
			wantKeyPresent: false,
		},
		{
			name:            "single download sends work_session when set",
			sources:         []string{"my-server:/path/file.txt"},
			downloadPath:    "/api/webftp/downloads/",
			workSessionID:   "ws-uuid-single",
			wantKeyPresent:  true,
			wantWorkSession: "ws-uuid-single",
		},
		{
			name:           "bulk download omits work_session when empty",
			sources:        []string{"my-server:/path/file1.txt", "my-server:/path/file2.txt"},
			downloadPath:   "/api/webftp/downloads/bulk/",
			workSessionID:  "",
			wantKeyPresent: false,
		},
		{
			name:            "bulk download sends work_session when set",
			sources:         []string{"my-server:/path/file1.txt", "my-server:/path/file2.txt"},
			downloadPath:    "/api/webftp/downloads/bulk/",
			workSessionID:   "ws-uuid-bulk",
			wantKeyPresent:  true,
			wantWorkSession: "ws-uuid-bulk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawBody map[string]any

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/api/servers/servers"):
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(serverResp)
				case r.URL.Path == tt.downloadPath && r.Method == http.MethodPost:
					body, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(body, &rawBody)
					// Stop the flow after capturing the body.
					w.WriteHeader(http.StatusBadRequest)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			// Error expected because the mock short-circuits after capturing the body;
			// we only assert on the captured request body.
			_ = DownloadFile(ac, tt.sources, "/tmp/dest", "", "", false, tt.workSessionID)

			v, present := rawBody["work_session"]
			assert.Equal(t, tt.wantKeyPresent, present)
			if tt.wantKeyPresent {
				assert.Equal(t, tt.wantWorkSession, v)
			}
		})
	}
}
