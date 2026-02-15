package packages

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
)

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
