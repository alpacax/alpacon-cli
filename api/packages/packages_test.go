package packages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

type (
	roundTripFunc func(*http.Request) (*http.Response, error)

	failingReadCloser struct {
		reader *bytes.Reader
	}
)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, io.ErrUnexpectedEOF
}

func (r *failingReadCloser) Close() error {
	return nil
}

func testResponse(statusCode int, contentType string, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       body,
	}
}

func jsonTestResponse(statusCode int, body string) *http.Response {
	return testResponse(statusCode, "application/json", io.NopCloser(bytes.NewBufferString(body)))
}

func TestGetSystemPackageEntry_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []SystemPackageDetail
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, SystemPackageDetail{
					ID:   fmt.Sprintf("spkg-%d", i),
					Name: fmt.Sprintf("sys-pkg-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, SystemPackageDetail{
					ID:   fmt.Sprintf("spkg-p2-%d", i),
					Name: fmt.Sprintf("sys-pkg-p2-%d", i),
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[SystemPackageDetail]{
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

	packages, err := GetSystemPackageEntry(ac)
	if err != nil {
		t.Fatalf("GetSystemPackageEntry error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(packages) != 150 {
		t.Errorf("expected 150 packages, got %d", len(packages))
	}
}

func TestUploadPackage_MultipartFromFile(t *testing.T) {
	var uploadedName string
	var uploadedContent string
	var contentLength int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/packages/python/entries/" {
			http.NotFound(w, r)
			return
		}

		contentLength = r.ContentLength
		partReader, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		part, err := partReader.NextPart()
		if err != nil {
			t.Errorf("NextPart error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = part.Close() }()

		uploadedName = part.FileName()
		body, err := io.ReadAll(part)
		if err != nil {
			t.Errorf("ReadAll error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		uploadedContent = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	packagePath := filepath.Join(t.TempDir(), "pkg.whl")
	if err := os.WriteFile(packagePath, []byte("package-content"), 0644); err != nil {
		t.Fatalf("failed to write test package: %v", err)
	}

	if err := UploadPackage(ac, packagePath, "python"); err != nil {
		t.Fatalf("UploadPackage error: %v", err)
	}
	if uploadedName != filepath.Base(packagePath) {
		t.Errorf("expected uploaded filename %q, got %q", filepath.Base(packagePath), uploadedName)
	}
	if uploadedContent != "package-content" {
		t.Errorf("expected uploaded content %q, got %q", "package-content", uploadedContent)
	}
	if contentLength <= int64(len("package-content")) {
		t.Errorf("expected multipart content length to be set, got %d", contentLength)
	}
}

func TestDownloadPackage_StreamsToFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/":
			_ = json.NewEncoder(w).Encode(api.ListResponse[PythonPackageDetail]{
				Count:   1,
				Results: []PythonPackageDetail{{ID: "pkg-id", Name: "pkg.whl"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/pkg-id/":
			_ = json.NewEncoder(w).Encode(DownloadURL{
				DownloadURL: "http://" + r.Host + "/api/download/pkg.whl",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/download/pkg.whl":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("downloaded-package"))
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
	if err := DownloadPackage(ac, "pkg.whl", dest, "python"); err != nil {
		t.Fatalf("DownloadPackage error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dest, "pkg.whl"))
	if err != nil {
		t.Fatalf("failed to read downloaded package: %v", err)
	}
	if string(content) != "downloaded-package" {
		t.Errorf("expected downloaded content %q, got %q", "downloaded-package", string(content))
	}
}

func TestDownloadPackage_StatusErrorDoesNotWriteFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/":
			_ = json.NewEncoder(w).Encode(api.ListResponse[PythonPackageDetail]{
				Count:   1,
				Results: []PythonPackageDetail{{ID: "pkg-id", Name: "pkg.whl"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/pkg-id/":
			_ = json.NewEncoder(w).Encode(DownloadURL{
				DownloadURL: "http://" + r.Host + "/api/download/pkg.whl",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/download/pkg.whl":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
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
	err := DownloadPackage(ac, "pkg.whl", dest, "python")
	if err == nil {
		t.Fatal("expected DownloadPackage to fail")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "pkg.whl")); !os.IsNotExist(statErr) {
		t.Fatalf("expected package file not to be written, stat error: %v", statErr)
	}
}

func TestDownloadPackage_NoContentDoesNotOverwriteExistingFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/":
			_ = json.NewEncoder(w).Encode(api.ListResponse[PythonPackageDetail]{
				Count:   1,
				Results: []PythonPackageDetail{{ID: "pkg-id", Name: "pkg.whl"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/pkg-id/":
			_ = json.NewEncoder(w).Encode(DownloadURL{
				DownloadURL: "http://" + r.Host + "/api/download/pkg.whl",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/download/pkg.whl":
			w.WriteHeader(http.StatusNoContent)
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
	savePath := filepath.Join(dest, "pkg.whl")
	if err := os.WriteFile(savePath, []byte("existing-package"), 0644); err != nil {
		t.Fatalf("failed to write existing package: %v", err)
	}

	err := DownloadPackage(ac, "pkg.whl", dest, "python")
	if err == nil {
		t.Fatal("expected DownloadPackage to fail")
	}

	content, readErr := os.ReadFile(savePath)
	if readErr != nil {
		t.Fatalf("failed to read existing package after failed download: %v", readErr)
	}
	if string(content) != "existing-package" {
		t.Fatalf("expected existing package to remain intact, got %q", string(content))
	}
}

func TestDownloadPackage_JSONSuccessDoesNotWriteFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/":
			_ = json.NewEncoder(w).Encode(api.ListResponse[PythonPackageDetail]{
				Count:   1,
				Results: []PythonPackageDetail{{ID: "pkg-id", Name: "pkg.whl"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/packages/python/entries/pkg-id/":
			_ = json.NewEncoder(w).Encode(DownloadURL{
				DownloadURL: "http://" + r.Host + "/api/download/pkg.whl",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/download/pkg.whl":
			_, _ = w.Write([]byte(`{"detail":"not ready"}`))
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
	err := DownloadPackage(ac, "pkg.whl", dest, "python")
	if err == nil {
		t.Fatal("expected DownloadPackage to fail")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "pkg.whl")); !os.IsNotExist(statErr) {
		t.Fatalf("expected package file not to be written, stat error: %v", statErr)
	}
}

func TestDownloadPackage_ReadErrorKeepsExistingFile(t *testing.T) {
	ac := &client.AlpaconClient{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/api/packages/python/entries/":
					return jsonTestResponse(http.StatusOK, `{"count":1,"results":[{"id":"pkg-id","name":"pkg.whl"}]}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/api/packages/python/entries/pkg-id/":
					return jsonTestResponse(http.StatusOK, `{"download_url":"https://example.test/api/download/pkg.whl"}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/api/download/pkg.whl":
					return testResponse(http.StatusOK, "application/octet-stream", &failingReadCloser{
						reader: bytes.NewReader([]byte("partial-package")),
					}), nil
				default:
					return jsonTestResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
				}
			}),
		},
		BaseURL: "https://example.test",
	}

	dest := t.TempDir()
	savePath := filepath.Join(dest, "pkg.whl")
	if err := os.WriteFile(savePath, []byte("existing-package"), 0644); err != nil {
		t.Fatalf("failed to write existing package: %v", err)
	}

	err := DownloadPackage(ac, "pkg.whl", dest, "python")
	if err == nil {
		t.Fatal("expected DownloadPackage to fail")
	}

	content, readErr := os.ReadFile(savePath)
	if readErr != nil {
		t.Fatalf("failed to read existing package after failed download: %v", readErr)
	}
	if string(content) != "existing-package" {
		t.Fatalf("expected existing package to remain intact, got %q", string(content))
	}

	matches, globErr := filepath.Glob(filepath.Join(dest, ".alpacon-*.tmp"))
	if globErr != nil {
		t.Fatalf("failed to inspect temp files: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("expected temp files to be cleaned up, found %v", matches)
	}
}

func TestGetPythonPackageEntry_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page=%s)", count, r.URL.Query().Get("page"))
			return
		}

		page := r.URL.Query().Get("page")
		t.Logf("request #%d: page=%s", count, page)

		var results []PythonPackageDetail
		switch page {
		case "1", "":
			for i := 0; i < 100; i++ {
				results = append(results, PythonPackageDetail{
					ID:   fmt.Sprintf("ppkg-%d", i),
					Name: fmt.Sprintf("py-pkg-%d", i),
				})
			}
		case "2":
			for i := 0; i < 50; i++ {
				results = append(results, PythonPackageDetail{
					ID:   fmt.Sprintf("ppkg-p2-%d", i),
					Name: fmt.Sprintf("py-pkg-p2-%d", i),
				})
			}
		}

		var next int
		if page == "1" || page == "" {
			next = 2
		}
		resp := api.ListResponse[PythonPackageDetail]{
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

	packages, err := GetPythonPackageEntry(ac)
	if err != nil {
		t.Fatalf("GetPythonPackageEntry error: %v", err)
	}

	totalRequests := int(requestCount.Load())
	if totalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", totalRequests)
	}
	if len(packages) != 150 {
		t.Errorf("expected 150 packages, got %d", len(packages))
	}
}
